package ufs

import (
	"fmt"
	"path"
	"strings"

	"github.com/mvslovers/ufsd-utils/pkg/ebcdic"
)

// MkDir creates a new directory at the given path.
// Parent directories must already exist.
func (img *Image) MkDir(dirPath, owner, group string) error {
	parentPath, name := splitPath(dirPath)
	if name == "" {
		return fmt.Errorf("invalid path: %q", dirPath)
	}
	if len(name) > NameMax {
		return fmt.Errorf("name too long: %q (%d > %d)", name, len(name), NameMax)
	}

	parentIno, err := img.ResolvePath(parentPath)
	if err != nil {
		return fmt.Errorf("parent %q: %w", parentPath, err)
	}

	// Check parent is a directory
	parentDi, err := img.ReadInode(parentIno)
	if err != nil {
		return err
	}
	if parentDi.Mode&IFMT != IFDIR {
		return fmt.Errorf("%q is not a directory", parentPath)
	}

	// Check name doesn't already exist
	entries, err := img.ReadDir(parentIno)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.NameString() == name {
			return fmt.Errorf("%q already exists", dirPath)
		}
	}

	// Allocate inode and data block
	ino, err := img.AllocInode()
	if err != nil {
		return fmt.Errorf("alloc inode: %w", err)
	}

	block, err := img.AllocBlock()
	if err != nil {
		return fmt.Errorf("alloc block: %w", err)
	}

	now := TimeNow()

	// Write directory data block (. and ..)
	dirBuf := make([]byte, img.blkSize)
	be.PutUint32(dirBuf[0:], ino)
	copy(dirBuf[4:], ebcdic.Encode(".", 60))
	be.PutUint32(dirBuf[64:], parentIno)
	copy(dirBuf[68:], ebcdic.Encode("..", 60))

	if err := img.WriteSector(block, dirBuf); err != nil {
		return fmt.Errorf("write dir block: %w", err)
	}

	// Write new directory inode
	di := &DiskInode{
		Mode:     IFDIR | DefaultUmask,
		NLink:    2,
		FileSize: DirentSize * 2,
		CTime:    now,
		MTime:    now,
		ATime:    now,
	}
	copy(di.Owner[:], ebcdic.Encode(owner, 9))
	copy(di.Group[:], ebcdic.Encode(group, 9))
	di.Addr[0] = block

	if err := img.WriteInode(ino, di); err != nil {
		return fmt.Errorf("write inode: %w", err)
	}

	// Add entry to parent directory
	if err := img.addDirEntry(parentIno, name, ino); err != nil {
		return fmt.Errorf("add dir entry: %w", err)
	}

	return img.FlushSuperBlock()
}

// MkDirAll creates a directory and all missing parent directories,
// similar to `mkdir -p`.
func (img *Image) MkDirAll(dirPath, owner, group string) error {
	// Walk path components, create each if missing
	parts := strings.Split(strings.Trim(dirPath, "/"), "/")
	current := "/"

	for _, part := range parts {
		if part == "" {
			continue
		}
		next := path.Join(current, part)
		if next[0] != '/' {
			next = "/" + next
		}

		// Check if it exists
		_, err := img.ResolvePath(next)
		if err != nil {
			// Doesn't exist — create it
			if err := img.MkDir(next, owner, group); err != nil {
				return err
			}
		}
		current = next
	}
	return nil
}

// CreateFile creates a new file and writes the given data.
// Parent directory must exist.
func (img *Image) CreateFile(filePath string, data []byte, owner, group string) error {
	parentPath, name := splitPath(filePath)
	if name == "" {
		return fmt.Errorf("invalid path: %q", filePath)
	}
	if len(name) > NameMax {
		return fmt.Errorf("name too long: %q (%d > %d)", name, len(name), NameMax)
	}

	parentIno, err := img.ResolvePath(parentPath)
	if err != nil {
		return fmt.Errorf("parent %q: %w", parentPath, err)
	}

	// Check parent is a directory
	parentDi, err := img.ReadInode(parentIno)
	if err != nil {
		return err
	}
	if parentDi.Mode&IFMT != IFDIR {
		return fmt.Errorf("%q is not a directory", parentPath)
	}

	// Check name doesn't already exist
	entries, err := img.ReadDir(parentIno)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.NameString() == name {
			return fmt.Errorf("%q already exists in %q", name, parentPath)
		}
	}

	// Allocate inode
	ino, err := img.AllocInode()
	if err != nil {
		return fmt.Errorf("alloc inode: %w", err)
	}

	now := TimeNow()

	// Calculate how many blocks we need
	dataLen := uint32(len(data))
	nBlocks := (dataLen + img.blkSize - 1) / img.blkSize
	if dataLen == 0 {
		nBlocks = 0
	}

	// Phase 1 limitation: direct blocks only
	if nBlocks > NAddrDirect {
		return fmt.Errorf("file too large: %d bytes requires %d blocks (max %d with direct addressing = %d bytes)",
			dataLen, nBlocks, NAddrDirect, NAddrDirect*img.blkSize)
	}

	// Allocate and write data blocks
	di := &DiskInode{
		Mode:     IFREG | DefaultUmask,
		NLink:    1,
		FileSize: dataLen,
		CTime:    now,
		MTime:    now,
		ATime:    now,
	}
	copy(di.Owner[:], ebcdic.Encode(owner, 9))
	copy(di.Group[:], ebcdic.Encode(group, 9))

	buf := make([]byte, img.blkSize)
	written := uint32(0)

	for i := uint32(0); i < nBlocks; i++ {
		block, err := img.AllocBlock()
		if err != nil {
			return fmt.Errorf("alloc data block %d: %w", i, err)
		}
		di.Addr[i] = block

		// Fill block with data
		for j := range buf {
			buf[j] = 0
		}
		chunk := img.blkSize
		if written+chunk > dataLen {
			chunk = dataLen - written
		}
		copy(buf, data[written:written+chunk])
		written += chunk

		if err := img.WriteSector(block, buf); err != nil {
			return fmt.Errorf("write data block %d: %w", i, err)
		}
	}

	// Write inode
	if err := img.WriteInode(ino, di); err != nil {
		return fmt.Errorf("write inode: %w", err)
	}

	// Add entry to parent directory
	if err := img.addDirEntry(parentIno, name, ino); err != nil {
		return fmt.Errorf("add dir entry: %w", err)
	}

	return img.FlushSuperBlock()
}

// ReadFileData reads all data from a regular file inode.
func (img *Image) ReadFileData(ino uint32) ([]byte, error) {
	di, err := img.ReadInode(ino)
	if err != nil {
		return nil, err
	}
	if di.Mode&IFMT != IFREG {
		return nil, fmt.Errorf("inode %d is not a regular file", ino)
	}
	if di.FileSize == 0 {
		return []byte{}, nil
	}

	data := make([]byte, di.FileSize)
	buf := make([]byte, img.blkSize)
	read := uint32(0)

	nBlocks := (di.FileSize + img.blkSize - 1) / img.blkSize

	for i := uint32(0); i < nBlocks && i < NAddrDirect; i++ {
		if di.Addr[i] == 0 {
			// Sparse hole — already zero in data[]
			read += img.blkSize
			continue
		}

		if err := img.ReadSector(di.Addr[i], buf); err != nil {
			return nil, fmt.Errorf("read block %d (sector %d): %w", i, di.Addr[i], err)
		}

		chunk := img.blkSize
		if read+chunk > di.FileSize {
			chunk = di.FileSize - read
		}
		copy(data[read:read+chunk], buf[:chunk])
		read += chunk
	}

	return data, nil
}

// ResolvePath walks the directory tree from root to find the target inode.
// Exported for use by CLI commands.
func (img *Image) ResolvePath(p string) (uint32, error) {
	if p == "" || p == "/" {
		return InodeRoot, nil
	}
	p = strings.TrimPrefix(p, "/")
	parts := strings.Split(p, "/")
	currentIno := uint32(InodeRoot)

	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			// Read current dir for ".." entry
			entries, err := img.ReadDir(currentIno)
			if err != nil {
				return 0, err
			}
			found := false
			for _, de := range entries {
				if de.NameString() == ".." {
					currentIno = de.InodeNumber
					found = true
					break
				}
			}
			if !found {
				return 0, fmt.Errorf("no '..' in directory")
			}
			continue
		}

		entries, err := img.ReadDir(currentIno)
		if err != nil {
			return 0, fmt.Errorf("reading directory: %w", err)
		}
		found := false
		for _, de := range entries {
			if de.NameString() == part {
				currentIno = de.InodeNumber
				found = true
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("%q not found", part)
		}
	}
	return currentIno, nil
}

// addDirEntry adds a new directory entry to the given directory inode.
// Expands the directory if needed (allocates new block).
func (img *Image) addDirEntry(dirIno uint32, name string, childIno uint32) error {
	di, err := img.ReadInode(dirIno)
	if err != nil {
		return err
	}

	nde := img.blkSize / DirentSize
	nBlocks := (di.FileSize + img.blkSize - 1) / img.blkSize
	buf := make([]byte, img.blkSize)

	// Try to find a free slot (inode_number == 0) in existing blocks
	for i := uint32(0); i < nBlocks && i < NAddrDirect; i++ {
		if di.Addr[i] == 0 {
			continue
		}
		if err := img.ReadSector(di.Addr[i], buf); err != nil {
			continue
		}

		for j := uint32(0); j < nde; j++ {
			off := j * DirentSize
			ino := be.Uint32(buf[off:])
			if ino == 0 {
				// Free slot — use it
				be.PutUint32(buf[off:], childIno)
				encoded := ebcdic.Encode(name, 60)
				copy(buf[off+4:off+64], encoded)
				return img.WriteSector(di.Addr[i], buf)
			}
		}
	}

	// No free slot — need to expand directory with a new block
	if nBlocks >= NAddrDirect {
		return fmt.Errorf("directory full (max %d blocks)", NAddrDirect)
	}

	block, err := img.AllocBlock()
	if err != nil {
		return fmt.Errorf("alloc dir block: %w", err)
	}

	// Write the new entry in the new block
	for i := range buf {
		buf[i] = 0
	}
	be.PutUint32(buf[0:], childIno)
	copy(buf[4:64], ebcdic.Encode(name, 60))

	if err := img.WriteSector(block, buf); err != nil {
		return err
	}

	// Update directory inode
	di.Addr[nBlocks] = block
	di.FileSize += img.blkSize
	di.MTime = TimeNow()
	return img.WriteInode(dirIno, di)
}

// splitPath returns the parent path and the final component.
// "/foo/bar/baz" -> ("/foo/bar", "baz")
// "/foo" -> ("/", "foo")
func splitPath(p string) (string, string) {
	p = strings.TrimRight(p, "/")
	if p == "" || p == "/" {
		return "/", ""
	}
	dir := path.Dir(p)
	base := path.Base(p)
	if dir == "." {
		dir = "/"
	}
	return dir, base
}
