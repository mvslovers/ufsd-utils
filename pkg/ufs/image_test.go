package ufs

import (
	"os"
	"path/filepath"
	"testing"
)

func tempImage(t *testing.T, size int64, blksize uint32) (*Image, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.img")
	img, err := Create(path, size, blksize, 10.0, "TEST", "GRP")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return img, path
}

func TestCreateAndOpen(t *testing.T) {
	img, path := tempImage(t, 1024*1024, 4096)
	img.Close()

	img2, err := Open(path, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer img2.Close()

	if img2.BlkSize() != 4096 {
		t.Errorf("BlkSize = %d, want 4096", img2.BlkSize())
	}
	sb := img2.SB()
	if sb.VolumeSize == 0 {
		t.Error("VolumeSize = 0")
	}
	if sb.DataBlockStart < IListSector+2 {
		t.Errorf("DataBlockStart = %d, too low", sb.DataBlockStart)
	}
}

func TestCreateInvalidBlockSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.img")
	_, err := Create(path, 1024*1024, 100, 10.0, "", "")
	if err == nil {
		t.Error("expected error for invalid block size")
	}
	os.Remove(path)
}

func TestRootDirectory(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	entries, err := img.ReadDir(InodeRoot)
	if err != nil {
		t.Fatalf("ReadDir(root): %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("root dir has %d entries, want >= 2", len(entries))
	}

	names := make(map[string]bool)
	for _, e := range entries {
		names[e.NameString()] = true
	}
	if !names["."] {
		t.Error("root dir missing '.'")
	}
	if !names[".."] {
		t.Error("root dir missing '..'")
	}
}

func TestCreateFileAndReadBack(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	data := []byte("Hello, UFS370!")
	if err := img.CreateFile("/test.txt", data, "USR", "GRP"); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	ino, err := img.ResolvePath("/test.txt")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}

	got, err := img.ReadFileData(ino)
	if err != nil {
		t.Fatalf("ReadFileData: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("ReadFileData = %q, want %q", got, data)
	}
}

func TestCreateFileEmptyData(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	if err := img.CreateFile("/empty.txt", []byte{}, "USR", "GRP"); err != nil {
		t.Fatalf("CreateFile empty: %v", err)
	}

	ino, err := img.ResolvePath("/empty.txt")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}

	got, err := img.ReadFileData(ino)
	if err != nil {
		t.Fatalf("ReadFileData: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty file: got %d bytes, want 0", len(got))
	}
}

func TestMkDirAndResolve(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	if err := img.MkDir("/subdir", "USR", "GRP"); err != nil {
		t.Fatalf("MkDir: %v", err)
	}

	ino, err := img.ResolvePath("/subdir")
	if err != nil {
		t.Fatalf("ResolvePath(/subdir): %v", err)
	}

	di, err := img.ReadInode(ino)
	if err != nil {
		t.Fatalf("ReadInode: %v", err)
	}
	if di.Mode&IFMT != IFDIR {
		t.Errorf("mode = 0x%04X, not a directory", di.Mode)
	}
}

func TestMkDirAll(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	if err := img.MkDirAll("/a/b/c", "USR", "GRP"); err != nil {
		t.Fatalf("MkDirAll: %v", err)
	}

	for _, p := range []string{"/a", "/a/b", "/a/b/c"} {
		if _, err := img.ResolvePath(p); err != nil {
			t.Errorf("ResolvePath(%q) after MkDirAll: %v", p, err)
		}
	}
}

func TestFileInSubdir(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	if err := img.MkDir("/web", "USR", "GRP"); err != nil {
		t.Fatalf("MkDir: %v", err)
	}

	data := []byte("<html>test</html>")
	if err := img.CreateFile("/web/index.html", data, "USR", "GRP"); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	ino, err := img.ResolvePath("/web/index.html")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}

	got, err := img.ReadFileData(ino)
	if err != nil {
		t.Fatalf("ReadFileData: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestDuplicateNameFails(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	if err := img.CreateFile("/dup.txt", []byte("a"), "USR", "GRP"); err != nil {
		t.Fatalf("first CreateFile: %v", err)
	}
	if err := img.CreateFile("/dup.txt", []byte("b"), "USR", "GRP"); err == nil {
		t.Error("duplicate CreateFile should fail")
	}
}

// TestFreeInodeCachePopulated verifies that Create() populates the
// superblock free inode cache (fixes UFSD seeing 0 free inodes).
func TestFreeInodeCachePopulated(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	sb := img.SB()
	if sb.NFreeInode == 0 {
		t.Fatal("NFreeInode = 0 in superblock (free inode cache not populated)")
	}
	// First free inode should be 3 (0=reserved, 1=BALBLK, 2=root)
	if sb.FreeInode[0] != 3 {
		t.Errorf("FreeInode[0] = %d, want 3", sb.FreeInode[0])
	}
	// NFreeInode should be > 0 and <= MaxFreeInode
	if sb.NFreeInode > MaxFreeInode {
		t.Errorf("NFreeInode = %d, exceeds MaxFreeInode = %d",
			sb.NFreeInode, MaxFreeInode)
	}
	// All cached inodes should be valid (> BALBLK)
	for i := uint32(0); i < sb.NFreeInode; i++ {
		if sb.FreeInode[i] <= InodeBALBLK {
			t.Errorf("FreeInode[%d] = %d (should be > %d)", i, sb.FreeInode[i], InodeBALBLK)
		}
	}
}

func TestResolvePathDotDot(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	if err := img.MkDir("/dir1", "USR", "GRP"); err != nil {
		t.Fatal(err)
	}

	ino, err := img.ResolvePath("/dir1/..")
	if err != nil {
		t.Fatalf("ResolvePath('/dir1/..'): %v", err)
	}
	if ino != InodeRoot {
		t.Errorf("'/dir1/..' resolved to inode %d, want %d (root)", ino, InodeRoot)
	}
}

// TestCreateLargeFileIndirect verifies single indirect block support
// for files > 64KB (> 16 direct blocks at 4096 blocksize).
func TestCreateLargeFileIndirect(t *testing.T) {
	// 2MB image, enough room for a ~100KB file
	img, _ := tempImage(t, 2*1024*1024, 4096)
	defer img.Close()

	// Create a 100KB file (25 blocks — 16 direct + 9 indirect)
	size := 100 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251) // deterministic pattern
	}

	if err := img.CreateFile("/large.bin", data, "USR", "GRP"); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	// Verify inode has indirect block set
	ino, err := img.ResolvePath("/large.bin")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	di, err := img.ReadInode(ino)
	if err != nil {
		t.Fatalf("ReadInode: %v", err)
	}
	if di.FileSize != uint32(size) {
		t.Errorf("FileSize = %d, want %d", di.FileSize, size)
	}
	if di.Addr[NAddrIndex1] == 0 {
		t.Error("Addr[16] (indirect block) is 0, expected non-zero")
	}

	// Read back and compare
	got, err := img.ReadFileData(ino)
	if err != nil {
		t.Fatalf("ReadFileData: %v", err)
	}
	if len(got) != len(data) {
		t.Fatalf("ReadFileData length = %d, want %d", len(got), len(data))
	}
	for i := range data {
		if got[i] != data[i] {
			t.Errorf("byte mismatch at offset %d: got 0x%02X, want 0x%02X", i, got[i], data[i])
			break
		}
	}
}

// TestSmallFileNoIndirect verifies that files <= 64KB use only direct blocks.
func TestSmallFileNoIndirect(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	// Create a 64KB file (exactly 16 blocks — all direct)
	data := make([]byte, 16*4096)
	for i := range data {
		data[i] = byte(i % 199)
	}

	if err := img.CreateFile("/exact16.bin", data, "USR", "GRP"); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	ino, err := img.ResolvePath("/exact16.bin")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	di, err := img.ReadInode(ino)
	if err != nil {
		t.Fatalf("ReadInode: %v", err)
	}
	if di.Addr[NAddrIndex1] != 0 {
		t.Error("Addr[16] should be 0 for file that fits in direct blocks")
	}

	got, err := img.ReadFileData(ino)
	if err != nil {
		t.Fatalf("ReadFileData: %v", err)
	}
	if len(got) != len(data) {
		t.Fatalf("length = %d, want %d", len(got), len(data))
	}
	for i := range data {
		if got[i] != data[i] {
			t.Errorf("byte mismatch at offset %d", i)
			break
		}
	}
}

// TestFileTooLargeForSingleIndirect verifies rejection of files > ~4MB.
func TestFileTooLargeForSingleIndirect(t *testing.T) {
	img, _ := tempImage(t, 1024*1024, 4096)
	defer img.Close()

	// 4MB + 4KB = exceeds single indirect capacity
	maxBlocks := uint32(NAddrDirect) + 4096/4
	tooBig := make([]byte, (maxBlocks+1)*4096)

	err := img.CreateFile("/huge.bin", tooBig, "USR", "GRP")
	if err == nil {
		t.Error("expected error for file exceeding single indirect capacity")
	}
}

func TestBlockSizes(t *testing.T) {
	for _, bs := range []uint32{512, 1024, 2048, 4096, 8192} {
		t.Run("blksize="+string(rune('0'+bs/512)), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "test.img")
			img, err := Create(path, 512*1024, bs, 10.0, "TST", "GRP")
			if err != nil {
				t.Fatalf("Create(blksize=%d): %v", bs, err)
			}

			data := []byte("test data for block size validation")
			if err := img.CreateFile("/test.txt", data, "TST", "GRP"); err != nil {
				t.Fatalf("CreateFile: %v", err)
			}

			ino, _ := img.ResolvePath("/test.txt")
			got, err := img.ReadFileData(ino)
			if err != nil {
				t.Fatalf("ReadFileData: %v", err)
			}
			if string(got) != string(data) {
				t.Errorf("roundtrip failed at blksize=%d", bs)
			}
			img.Close()
		})
	}
}
