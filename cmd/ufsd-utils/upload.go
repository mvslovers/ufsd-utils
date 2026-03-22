package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mvslovers/ufsd-utils/pkg/ufs"
)

func cmdUpload(args []string) {
	fs := flag.NewFlagSet("upload", flag.ExitOnError)
	dsn := fs.String("dsn", "", "Target dataset name on MVS (required)")
	host := fs.String("host", "", "MVS host (overrides MVS_HOST)")
	port := fs.String("port", "", "MVS port (overrides MVS_PORT)")
	replace := fs.Bool("replace", false, "Overwrite existing dataset")

	fs.Usage = func() {
		fmt.Print(`Usage:
  ufsd-utils upload [options] <image-file>

Upload a UFS370 disk image to MVS as a sequential dataset via the
zOSMF REST API. Allocates the dataset automatically if it does not
exist (RECFM=U, DSORG=PS, BLKSIZE from image superblock).

Authentication via environment variables (or .env file):
  MVS_HOST    MVS hostname or IP
  MVS_PORT    mvsMF API port (default: 1080)
  MVS_USER    MVS userid
  MVS_PASS    MVS password

Options:
`)
		fs.PrintDefaults()
	}
	fs.Parse(reorderArgs(args))

	if fs.NArg() < 1 || *dsn == "" {
		fs.Usage()
		os.Exit(1)
	}
	imgPath := fs.Arg(0)

	// Load .env, then override with explicit env vars
	loadDotEnv()

	cfg := mvsConfig{
		host: envOrDefault("MVS_HOST", ""),
		port: envOrDefault("MVS_PORT", "1080"),
		user: envOrDefault("MVS_USER", ""),
		pass: envOrDefault("MVS_PASS", ""),
	}
	if *host != "" {
		cfg.host = *host
	}
	if *port != "" {
		cfg.port = *port
	}
	if cfg.host == "" {
		die("MVS_HOST not set (use --host or MVS_HOST env var)")
	}
	if cfg.user == "" {
		die("MVS_USER not set")
	}
	if cfg.pass == "" {
		die("MVS_PASS not set")
	}

	// Open image and read metadata
	img, err := ufs.Open(imgPath, true)
	if err != nil {
		die("%v", err)
	}
	defer img.Close()

	sb := img.SB()
	blkSize := img.BlkSize()
	totalBlocks := sb.VolumeSize

	// Read entire image file
	data, err := os.ReadFile(imgPath)
	if err != nil {
		die("read image: %v", err)
	}

	*dsn = strings.ToUpper(*dsn)
	baseURL := fmt.Sprintf("http://%s:%s/zosmf", cfg.host, cfg.port)

	fmt.Printf("Upload %s -> %s (%d blocks, blksize=%d, %.2f MB)\n",
		imgPath, *dsn, totalBlocks, blkSize,
		float64(len(data))/1048576.0)

	// Check if dataset exists
	exists, err := datasetExists(baseURL, cfg, *dsn)
	if err != nil {
		die("check dataset: %v", err)
	}

	if exists {
		if !*replace {
			die("dataset %s already exists (use --replace to overwrite)", *dsn)
		}
		// Delete and recreate to ensure correct attributes
		fmt.Printf("  Deleting existing %s\n", *dsn)
		if err := deleteDataset(baseURL, cfg, *dsn); err != nil {
			die("delete dataset: %v", err)
		}
	}

	// Allocate dataset
	fmt.Printf("  Allocating %s (RECFM=U, BLKSIZE=%d, %d tracks)\n",
		*dsn, blkSize, totalBlocks)
	if err := allocateDataset(baseURL, cfg, *dsn, blkSize, totalBlocks); err != nil {
		die("allocate dataset: %v", err)
	}

	// Upload binary data
	fmt.Printf("  Uploading %d bytes\n", len(data))
	if err := uploadBinary(baseURL, cfg, *dsn, data); err != nil {
		die("upload: %v", err)
	}

	fmt.Println("Done.")
}

type mvsConfig struct {
	host string
	port string
	user string
	pass string
}

func datasetExists(baseURL string, cfg mvsConfig, dsn string) (bool, error) {
	// Use first 2 qualifiers for search
	parts := strings.Split(dsn, ".")
	n := len(parts) - 1
	if n > 2 {
		n = 2
	}
	if n < 1 {
		n = 1
	}
	searchLevel := strings.Join(parts[:n], ".")

	url := fmt.Sprintf("%s/restfiles/ds?dslevel=%s", baseURL, searchLevel)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, err
	}
	req.SetBasicAuth(cfg.user, cfg.pass)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return false, httpError("GET", url, resp.StatusCode, body)
	}

	var result struct {
		Items []struct {
			DSName string `json:"dsname"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("parse response: %w", err)
	}

	for _, item := range result.Items {
		if strings.TrimSpace(item.DSName) == dsn {
			return true, nil
		}
	}
	return false, nil
}

func allocateDataset(baseURL string, cfg mvsConfig, dsn string, blkSize, totalBlocks uint32) error {
	// Calculate tracks using blocks-per-track for 3350 (conservative).
	// 3350 track capacity: 19254 bytes. With RECFM=U and inter-block gaps,
	// integer division gives usable blocks per track.
	blocksPerTrack := uint32(19254) / blkSize
	if blocksPerTrack < 1 {
		blocksPerTrack = 1
	}
	tracks := (totalBlocks + blocksPerTrack - 1) / blocksPerTrack

	allocBody := fmt.Sprintf(
		`{"dsorg":"PS","alcunit":"TRK","primary":%d,"secondary":0,"recfm":"U","lrecl":%d,"blksize":%d}`,
		tracks, blkSize, blkSize)

	url := fmt.Sprintf("%s/restfiles/ds/%s", baseURL, dsn)
	req, err := http.NewRequest("POST", url, strings.NewReader(allocBody))
	if err != nil {
		return err
	}
	req.SetBasicAuth(cfg.user, cfg.pass)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return httpError("POST", url, resp.StatusCode, body)
	}
	return nil
}

func uploadBinary(baseURL string, cfg mvsConfig, dsn string, data []byte) error {
	url := fmt.Sprintf("%s/restfiles/ds/%s", baseURL, dsn)
	req, err := http.NewRequest("PUT", url, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.SetBasicAuth(cfg.user, cfg.pass)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-IBM-Data-Type", "binary")
	req.ContentLength = int64(len(data))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		return httpError("PUT", url, resp.StatusCode, body)
	}
	return nil
}

func deleteDataset(baseURL string, cfg mvsConfig, dsn string) error {
	url := fmt.Sprintf("%s/restfiles/ds/%s", baseURL, dsn)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cfg.user, cfg.pass)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		return httpError("DELETE", url, resp.StatusCode, body)
	}
	return nil
}

func httpError(method, url string, status int, body []byte) error {
	switch status {
	case 401:
		return fmt.Errorf("%s %s: authentication failed (HTTP 401) — check MVS_USER/MVS_PASS", method, url)
	case 403:
		return fmt.Errorf("%s %s: access denied (HTTP 403)", method, url)
	case 404:
		return fmt.Errorf("%s %s: not found (HTTP 404)", method, url)
	default:
		detail := strings.TrimSpace(string(body))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		return fmt.Errorf("%s %s: HTTP %d: %s", method, url, status, detail)
	}
}

// loadDotEnv reads a .env file from the current directory and sets
// environment variables that are not already set.
func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return // no .env file, that's fine
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		// Only set if not already in environment
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
