package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mvslovers/ufsd-utils/pkg/ebcdic"
	"github.com/mvslovers/ufsd-utils/pkg/ufs"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "create":
		cmdCreate(os.Args[2:])
	case "info":
		cmdInfo(os.Args[2:])
	case "ls":
		cmdLs(os.Args[2:])
	case "cp":
		cmdCp(os.Args[2:])
	case "cat":
		cmdCat(os.Args[2:])
	case "mkdir":
		cmdMkdir(os.Args[2:])
	case "version":
		fmt.Printf("ufsd-utils %s\n", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`ufsd-utils — Host-side tools for UFS370 disk images

Usage:
  ufsd-utils <command> [options] [arguments]

Commands:
  create   Create and format a new UFS370 disk image
  info     Display disk image metadata
  ls       List directory contents
  cp       Copy files between host and image
  cat      Display file contents from image
  mkdir    Create directory in image
  version  Show version
  help     Show this help

Examples:
  ufsd-utils create httpd-web.img --size 10M
  ufsd-utils info   httpd-web.img
  ufsd-utils ls -l  httpd-web.img /
  ufsd-utils mkdir  httpd-web.img /css
  ufsd-utils cp     ./index.html httpd-web.img:/index.html
  ufsd-utils cp -r  ./webroot/   httpd-web.img:/
  ufsd-utils cp     httpd-web.img:/index.html ./out.html
  ufsd-utils cat    httpd-web.img:/index.html
`)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

// --- create ---

func cmdCreate(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	size := fs.String("size", "10M", "Image size (e.g. 1M, 10M, 200M, 1G, 1MB)")
	blksize := fs.Uint("blksize", ufs.DefaultBlockSize, "Block size (512..8192)")
	fs.UintVar(blksize, "blocksize", ufs.DefaultBlockSize, "Block size (alias for --blksize)")
	inodePct := fs.Float64("inodes", 10.0, "Percentage of blocks for inodes")
	owner := fs.String("owner", "", "Root directory owner (default: current user or HERC01)")
	group := fs.String("group", "", "Root directory group (default: ADMIN)")

	fs.Usage = func() {
		fmt.Print("Usage: ufsd-utils create [options] <image-file>\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(reorderArgs(args))

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}
	path := fs.Arg(0)

	if *owner == "" {
		*owner = currentUser()
	}
	if *group == "" {
		*group = "ADMIN"
	}

	sizeBytes, err := parseSize(*size)
	if err != nil {
		die("invalid --size: %v", err)
	}

	fmt.Printf("Creating %s (%s, blksize=%d, inodes=%.1f%%)\n",
		path, *size, *blksize, *inodePct)

	img, err := ufs.Create(path, sizeBytes, uint32(*blksize), *inodePct, *owner, *group)
	if err != nil {
		die("%v", err)
	}
	defer img.Close()

	sb := img.SB()
	bs := img.BlkSize()
	inodeBlocks := sb.DataBlockStart - ufs.IListSector
	totalInodes := inodeBlocks * sb.InodesPerBlock

	fmt.Printf("  Volume size:     %d blocks (%.2f MB)\n",
		sb.VolumeSize, float64(sb.VolumeSize)*float64(bs)/1048576.0)
	fmt.Printf("  Block size:      %d bytes\n", bs)
	fmt.Printf("  Inode blocks:    %d (%d inodes)\n", inodeBlocks, totalInodes)
	fmt.Printf("  Data blocks:     %d (free: %d)\n",
		sb.VolumeSize-sb.DataBlockStart, sb.TotalFreeBlock)
	fmt.Printf("  Root owner:      %s/%s\n", *owner, *group)
	fmt.Println("  Format:          UFS370 v1 (time64 timestamps)")
	fmt.Println()
	printMVSAlloc(bs, sb.VolumeSize)
	fmt.Println("Done.")
}

// --- info ---

func cmdInfo(args []string) {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	fs.Usage = func() { fmt.Print("Usage: ufsd-utils info <image-file>\n") }
	fs.Parse(reorderArgs(args))

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	img, err := ufs.Open(fs.Arg(0), true)
	if err != nil {
		die("%v", err)
	}
	defer img.Close()

	boot := img.Boot()
	ext := img.BootExtension()
	sb := img.SB()
	blk := img.BlkSize()

	inodeBlocks := sb.DataBlockStart - ufs.IListSector
	totalInodes := inodeBlocks * sb.InodesPerBlock
	freeInodes := sb.TotalFreeInode
	usedInodes := totalInodes - freeInodes
	dataBlocks := sb.VolumeSize - sb.DataBlockStart
	freeBlocks := sb.TotalFreeBlock
	usedBlocks := dataBlocks - freeBlocks

	fmt.Printf("Image:           %s\n", fs.Arg(0))
	fmt.Printf("Format:          UFS370 v%d\n", ext.Version)
	fmt.Printf("Block size:      %d bytes\n", blk)
	fmt.Printf("Boot type:       %d (check: 0x%04X)\n", boot.Type, boot.Check)

	if ext.Version >= ufs.BootVersion1 {
		ct := time.UnixMilli(int64(ext.CreateTime))
		ut := time.UnixMilli(int64(ext.UpdateTime))
		fmt.Printf("Created:         %s\n", ct.UTC().Format("2006-01-02 15:04:05 UTC"))
		fmt.Printf("Updated:         %s\n", ut.UTC().Format("2006-01-02 15:04:05 UTC"))
	}

	fmt.Println()
	fmt.Printf("Volume size:     %d blocks (%.2f MB)\n",
		sb.VolumeSize, float64(sb.VolumeSize)*float64(blk)/1048576.0)
	fmt.Printf("Data blocks:     %d total, %d used, %d free (%.1f%% used)\n",
		dataBlocks, usedBlocks, freeBlocks,
		float64(usedBlocks)*100.0/float64(dataBlocks))
	fmt.Printf("Inodes:          %d total, %d used, %d free (%.1f%% used)\n",
		totalInodes, usedInodes, freeInodes,
		float64(usedInodes)*100.0/float64(totalInodes))
	fmt.Printf("Inode blocks:    %d (sector %d..%d)\n",
		inodeBlocks, ufs.IListSector, sb.DataBlockStart-1)
	fmt.Printf("Data start:      sector %d\n", sb.DataBlockStart)

	root, err := img.ReadInode(ufs.InodeRoot)
	if err == nil {
		owner := ebcdic.DecodeString(root.Owner[:])
		group := ebcdic.DecodeString(root.Group[:])
		fmt.Println()
		fmt.Printf("Root inode:      %d\n", ufs.InodeRoot)
		fmt.Printf("Root owner:      %s\n", owner)
		fmt.Printf("Root group:      %s\n", group)
		fmt.Printf("Root mode:       %04o\n", root.Mode&0xFFF)
		fmt.Printf("Root nlink:      %d\n", root.NLink)
		fmt.Printf("Root size:       %d bytes\n", root.FileSize)
		if !root.MTime.IsZero() {
			fmt.Printf("Root mtime:      %s\n", root.MTime.ToGo().UTC().Format("2006-01-02 15:04:05 UTC"))
		}
	}

	fmt.Println()
	printMVSAlloc(blk, sb.VolumeSize)
}

// --- ls ---

func cmdLs(args []string) {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	long := fs.Bool("l", false, "Long format")
	fs.Usage = func() { fmt.Print("Usage: ufsd-utils ls [-l] <image-file> [path]\n") }
	fs.Parse(reorderArgs(args))

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	imgPath := fs.Arg(0)
	dirPath := "/"
	if fs.NArg() > 1 {
		dirPath = fs.Arg(1)
	}

	img, err := ufs.Open(imgPath, true)
	if err != nil {
		die("%v", err)
	}
	defer img.Close()

	dirIno, err := img.ResolvePath(dirPath)
	if err != nil {
		die("path %q: %v", dirPath, err)
	}

	entries, err := img.ReadDir(dirIno)
	if err != nil {
		die("%v", err)
	}

	for _, de := range entries {
		name := de.NameString()
		if !*long {
			fmt.Println(name)
			continue
		}

		di, err := img.ReadInode(de.InodeNumber)
		if err != nil {
			fmt.Printf("??????????  ? ?        ?               ? %s\n", name)
			continue
		}

		modeStr := formatMode(di.Mode)
		owner := ebcdic.DecodeString(di.Owner[:])
		group := ebcdic.DecodeString(di.Group[:])
		mtime := di.MTime.ToGo()

		fmt.Printf("%s %3d %-8s %-8s %10d %s %s\n",
			modeStr, di.NLink, owner, group, di.FileSize,
			mtime.UTC().Format("2006 Jan 02 15:04"), name)
	}
}

// --- helpers ---

// --- cp ---

func cmdCp(args []string) {
	fs := flag.NewFlagSet("cp", flag.ExitOnError)
	recursive := fs.Bool("r", false, "Copy directories recursively")
	text := fs.Bool("t", false, "Text mode: convert ASCII<->EBCDIC (auto-detected by extension if not set)")
	binary := fs.Bool("b", false, "Binary mode: no conversion")
	owner := fs.String("owner", "", "Owner for created files (default: current user)")
	group := fs.String("group", "", "Group for created files (default: ADMIN)")

	fs.Usage = func() {
		fmt.Print(`Usage:
  ufsd-utils cp [options] <source> <dest>

Copy files between host filesystem and UFS370 disk image.
Image paths use the format: image.img:/path/in/image

Directions:
  host -> image:  ufsd-utils cp ./file.html image.img:/file.html
  image -> host:  ufsd-utils cp image.img:/file.html ./file.html
  recursive:      ufsd-utils cp -r ./webroot/ image.img:/

Text mode (-t) converts ASCII->EBCDIC when copying to image, and
EBCDIC->ASCII when copying from image. Auto-detected for common
extensions (.html, .txt, .css, .js, .xml, .json, .csv, .sh, .c, .h).
Use -b to force binary (no conversion).

Options:
`)
		fs.PrintDefaults()
	}
	fs.Parse(reorderArgs(args))

	if *owner == "" {
		*owner = currentUser()
	}
	if *group == "" {
		*group = "ADMIN"
	}

	if fs.NArg() < 2 {
		fs.Usage()
		os.Exit(1)
	}

	src := fs.Arg(0)
	dst := fs.Arg(1)

	srcImg, srcPath := parseImgPath(src)
	dstImg, dstPath := parseImgPath(dst)

	if srcImg != "" && dstImg != "" {
		die("cannot copy between two images (yet)")
	}
	if srcImg == "" && dstImg == "" {
		die("one of source or dest must be an image path (image.img:/path)")
	}

	if dstImg != "" {
		// Host -> Image
		cpHostToImage(src, dstImg, dstPath, *recursive, *text, *binary, *owner, *group)
	} else {
		// Image -> Host
		cpImageToHost(srcImg, srcPath, dst, *text, *binary)
	}
}

func cpHostToImage(hostPath, imgFile, imgPath string, recursive, textMode, binaryMode bool, owner, group string) {
	img, err := ufs.Open(imgFile, false)
	if err != nil {
		die("%v", err)
	}
	defer img.Close()

	info, err := os.Stat(hostPath)
	if err != nil {
		die("%v", err)
	}

	if info.IsDir() {
		if !recursive {
			die("%s is a directory (use -r)", hostPath)
		}
		cpDirToImage(img, hostPath, imgPath, textMode, binaryMode, owner, group, "")
		return
	}

	// Single file
	data, err := os.ReadFile(hostPath)
	if err != nil {
		die("read %s: %v", hostPath, err)
	}

	if shouldConvertText(hostPath, textMode, binaryMode) {
		data = ebcdic.ToEBCDIC(data)
	}

	// If imgPath ends with /, append the source filename
	if strings.HasSuffix(imgPath, "/") || imgPath == "/" {
		imgPath = imgPath + filepath_base(hostPath)
	}

	if err := img.CreateFile(imgPath, data, owner, group); err != nil {
		die("create %s: %v", imgPath, err)
	}
	fmt.Printf("  %s -> %s:%s (%d bytes)\n", hostPath, img.Path(), imgPath, len(data))
}

func cpDirToImage(img *ufs.Image, hostDir, imgDir string, textMode, binaryMode bool, owner, group, indent string) {
	entries, err := os.ReadDir(hostDir)
	if err != nil {
		die("read dir %s: %v", hostDir, err)
	}

	// Ensure target directory exists
	if imgDir != "/" {
		img.MkDirAll(imgDir, owner, group)
	}

	for _, e := range entries {
		hostPath := hostDir + "/" + e.Name()
		imgPath := imgDir
		if !strings.HasSuffix(imgPath, "/") {
			imgPath += "/"
		}
		imgPath += e.Name()

		if e.IsDir() {
			fmt.Printf("%s  mkdir %s\n", indent, imgPath)
			if err := img.MkDirAll(imgPath, owner, group); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: mkdir %s: %v\n", imgPath, err)
				continue
			}
			cpDirToImage(img, hostPath, imgPath, textMode, binaryMode, owner, group, indent+"  ")
			continue
		}

		data, err := os.ReadFile(hostPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: read %s: %v\n", hostPath, err)
			continue
		}

		isText := shouldConvertText(hostPath, textMode, binaryMode)
		if isText {
			data = ebcdic.ToEBCDIC(data)
		}

		if err := img.CreateFile(imgPath, data, owner, group); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: create %s: %v\n", imgPath, err)
			continue
		}

		mode := "bin"
		if isText {
			mode = "txt"
		}
		fmt.Printf("%s  %s -> %s (%d bytes, %s)\n", indent, e.Name(), imgPath, len(data), mode)
	}
}

func cpImageToHost(imgFile, imgPath, hostPath string, textMode, binaryMode bool) {
	img, err := ufs.Open(imgFile, true)
	if err != nil {
		die("%v", err)
	}
	defer img.Close()

	ino, err := img.ResolvePath(imgPath)
	if err != nil {
		die("resolve %s: %v", imgPath, err)
	}

	data, err := img.ReadFileData(ino)
	if err != nil {
		die("read %s: %v", imgPath, err)
	}

	if shouldConvertText(imgPath, textMode, binaryMode) {
		data = ebcdic.ToASCII(data)
	}

	if err := os.WriteFile(hostPath, data, 0644); err != nil {
		die("write %s: %v", hostPath, err)
	}
	fmt.Printf("  %s:%s -> %s (%d bytes)\n", imgFile, imgPath, hostPath, len(data))
}

// --- cat ---

func cmdCat(args []string) {
	fs := flag.NewFlagSet("cat", flag.ExitOnError)
	binary := fs.Bool("b", false, "Binary mode: no EBCDIC->ASCII conversion")
	fs.Usage = func() { fmt.Print("Usage: ufsd-utils cat [-b] <image.img:/path>\n") }
	fs.Parse(reorderArgs(args))

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	imgFile, imgPath := parseImgPath(fs.Arg(0))
	if imgFile == "" {
		die("argument must be image path (image.img:/path)")
	}

	img, err := ufs.Open(imgFile, true)
	if err != nil {
		die("%v", err)
	}
	defer img.Close()

	ino, err := img.ResolvePath(imgPath)
	if err != nil {
		die("resolve %s: %v", imgPath, err)
	}

	data, err := img.ReadFileData(ino)
	if err != nil {
		die("read %s: %v", imgPath, err)
	}

	if !*binary {
		data = ebcdic.ToASCII(data)
	}

	os.Stdout.Write(data)
}

// --- mkdir ---

func cmdMkdir(args []string) {
	fs := flag.NewFlagSet("mkdir", flag.ExitOnError)
	parents := fs.Bool("p", false, "Create parent directories as needed")
	owner := fs.String("owner", "", "Directory owner (default: current user)")
	group := fs.String("group", "", "Directory group (default: ADMIN)")
	fs.Usage = func() { fmt.Print("Usage: ufsd-utils mkdir [-p] [--owner X] [--group X] <image.img:/path>\n") }
	fs.Parse(reorderArgs(args))

	if *owner == "" {
		*owner = currentUser()
	}
	if *group == "" {
		*group = "ADMIN"
	}

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	imgFile, imgPath := parseImgPath(fs.Arg(0))
	if imgFile == "" {
		die("argument must be image path (image.img:/path)")
	}

	img, err := ufs.Open(imgFile, false)
	if err != nil {
		die("%v", err)
	}
	defer img.Close()

	if *parents {
		err = img.MkDirAll(imgPath, *owner, *group)
	} else {
		err = img.MkDir(imgPath, *owner, *group)
	}
	if err != nil {
		die("%v", err)
	}
	fmt.Printf("  created %s\n", imgPath)
}

// --- helpers ---

// parseImgPath splits "image.img:/path" into ("image.img", "/path").
// Returns ("", arg) if no colon-separator found.
func parseImgPath(arg string) (string, string) {
	// Look for the pattern: something.img:/path or something:/path
	// Avoid matching C:\path on Windows
	idx := strings.Index(arg, ":/")
	if idx < 1 {
		// Also try just ":" at end (e.g. "img.img:")
		if strings.HasSuffix(arg, ":") {
			return arg[:len(arg)-1], "/"
		}
		return "", arg
	}
	return arg[:idx], arg[idx+1:]
}

// shouldConvertText returns true if the file should be converted between
// ASCII and EBCDIC based on flags and file extension.
func shouldConvertText(path string, textFlag, binaryFlag bool) bool {
	if binaryFlag {
		return false
	}
	if textFlag {
		return true
	}
	// Auto-detect by extension
	ext := strings.ToLower(filepath_ext(path))
	switch ext {
	case ".html", ".htm", ".txt", ".css", ".js", ".xml", ".json",
		".csv", ".sh", ".c", ".h", ".s", ".md", ".cfg", ".conf",
		".toml", ".yaml", ".yml", ".ini", ".log", ".jcl", ".proc":
		return true
	}
	return false
}

// filepath_base returns the last element of path (like filepath.Base but
// without importing path/filepath to keep it simple).
func filepath_base(p string) string {
	if p == "" {
		return "."
	}
	// Strip trailing slashes
	for len(p) > 1 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// filepath_ext returns the file extension including the dot.
func filepath_ext(p string) string {
	base := filepath_base(p)
	if i := strings.LastIndex(base, "."); i >= 0 {
		return base[i:]
	}
	return ""
}

// printMVSAlloc prints MVS BDAM allocation parameters for the given
// block size and total block count.
func printMVSAlloc(blksize, totalBlocks uint32) {
	fmt.Println("  MVS Dataset Allocation (DSORG=DA, BDAM):")
	fmt.Println()
	fmt.Printf("    DCB=(DSORG=DA,BLKSIZE=%d)  SPACE=(%d,(%d))\n",
		blksize, blksize, totalBlocks)
	fmt.Println()
	fmt.Println("    JCL (recommended — ISPF/RPF panel cannot set DSORG=DA):")
	fmt.Printf("      //ALLOC  EXEC PGM=IEFBR14\n")
	fmt.Printf("      //DISK   DD DSN=your.dataset.name,\n")
	fmt.Printf("      //          DISP=(NEW,CATLG,DELETE),UNIT=SYSDA,\n")
	fmt.Printf("      //          SPACE=(%d,(%d)),\n", blksize, totalBlocks)
	fmt.Printf("      //          DCB=(DSORG=DA,BLKSIZE=%d)\n", blksize)
	fmt.Println()
	fmt.Println("    Transfer: FTP binary PUT into pre-allocated dataset")
}

// reorderArgs moves flags (--foo, -f, --foo=bar) before positional args,
// so that `create file.img --size 1M` works like `create --size 1M file.img`.
// Go's flag package stops parsing at the first non-flag argument.
func reorderArgs(args []string) []string {
	var flags, positional []string
	i := 0
	for i < len(args) {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			// If it's not --key=value, the next arg is the value
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positional = append(positional, args[i])
		}
		i++
	}
	return append(flags, positional...)
}

// currentUser returns the current OS username in uppercase,
// or "HERC01" as fallback.
func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return strings.ToUpper(u)
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return strings.ToUpper(u)
	}
	return "HERC01"
}

func formatMode(mode uint16) string {
	var buf [10]byte
	switch mode & ufs.IFMT {
	case ufs.IFDIR: buf[0] = 'd'
	case ufs.IFLNK: buf[0] = 'l'
	case ufs.IFCHR: buf[0] = 'c'
	case ufs.IFBLK: buf[0] = 'b'
	case ufs.IFIFO: buf[0] = 'p'
	case ufs.IFSCK: buf[0] = 's'
	default:         buf[0] = '-'
	}
	perms := "rwxrwxrwx"
	for i := 0; i < 9; i++ {
		if mode&(1<<uint(8-i)) != 0 {
			buf[1+i] = perms[i]
		} else {
			buf[1+i] = '-'
		}
	}
	return string(buf[:])
}

func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0, fmt.Errorf("empty size")
	}
	upper := strings.ToUpper(s)
	mul := int64(1)
	switch {
	case strings.HasSuffix(upper, "KB"):
		mul = 1024; s = s[:len(s)-2]
	case strings.HasSuffix(upper, "MB"):
		mul = 1024 * 1024; s = s[:len(s)-2]
	case strings.HasSuffix(upper, "GB"):
		mul = 1024 * 1024 * 1024; s = s[:len(s)-2]
	case strings.HasSuffix(upper, "K"):
		mul = 1024; s = s[:len(s)-1]
	case strings.HasSuffix(upper, "M"):
		mul = 1024 * 1024; s = s[:len(s)-1]
	case strings.HasSuffix(upper, "G"):
		mul = 1024 * 1024 * 1024; s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("size must be positive")
	}
	return n * mul, nil
}
