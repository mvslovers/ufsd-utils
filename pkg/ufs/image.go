package ufs

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/mvslovers/ufsd-utils/pkg/ebcdic"
)

// Image represents an open UFS370 disk image file.
type Image struct {
	file     *os.File
	path     string
	blkSize  uint32
	boot     BootBlock
	bootExt  BootExt
	sb       SuperBlock
	readOnly bool
}

// big-endian byte order for binary encoding
var be = binary.BigEndian

// Create creates a new UFS370 disk image, formats it, and returns the handle.
// sizeBytes is the desired image size; it will be rounded down to a whole
// number of blocks. inodePct controls what percentage of blocks are reserved
// for inodes (default 10.0). owner/group set the root directory inode fields.
func Create(path string, sizeBytes int64, blkSize uint32, inodePct float64,
	owner, group string) (*Image, error) {

	if blkSize < MinBlockSize || blkSize > MaxBlockSize || blkSize%512 != 0 {
		return nil, fmt.Errorf("invalid block size %d (must be 512..8192, multiple of 512)", blkSize)
	}
	if inodePct < 1.0 || inodePct > 50.0 {
		inodePct = 10.0
	}
	if owner == "" {
		owner = "HERC01"
	}
	if group == "" {
		group = "ADMIN"
	}

	totalBlocks := uint32(sizeBytes / int64(blkSize))
	if totalBlocks < 8 {
		return nil, fmt.Errorf("image too small: need at least 8 blocks (%d bytes)", 8*blkSize)
	}

	// Create the file
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}

	// Truncate to exact size
	imageSize := int64(totalBlocks) * int64(blkSize)
	if err := f.Truncate(imageSize); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("truncate: %w", err)
	}

	img := &Image{
		file:    f,
		path:    path,
		blkSize: blkSize,
	}

	// Phase 1: Write boot block
	if err := img.writeBootBlock(); err != nil {
		img.Close()
		os.Remove(path)
		return nil, fmt.Errorf("boot block: %w", err)
	}

	// Phase 2: Calculate geometry and write superblock
	inodesPerBlock := blkSize / InodeSize
	inodeBlocks := uint32(math.Round(float64(totalBlocks)/float64(inodesPerBlock)*inodePct/100.0))
	if inodeBlocks < 2 {
		inodeBlocks = 2
	}
	dataBlockStart := IListSector + inodeBlocks

	// Calculate blksize shift
	var shift uint32
	for n := blkSize; n > 1; n >>= 1 {
		shift++
	}

	img.sb = SuperBlock{
		DataBlockStart: dataBlockStart,
		VolumeSize:     totalBlocks,
		TotalFreeBlock: totalBlocks - dataBlockStart,
		TotalFreeInode: inodeBlocks*inodesPerBlock - 1, // minus BALBLK (inode 1); inode 0 has no physical slot
		InodesPerBlock: inodesPerBlock,
		BlkSizeShift:   shift,
		IListSector:    IListSector,
	}

	// Phase 3: Write inode blocks (all zeros = free)
	zeroBuf := make([]byte, blkSize)
	reservedBuf := make([]byte, blkSize)
	// First inode block: inodes 0 and 1 are reserved (fill with 0xFF)
	for i := uint32(0); i < InodeSize*2 && i < blkSize; i++ {
		reservedBuf[i] = 0xFF
	}
	if err := img.WriteSector(IListSector, reservedBuf); err != nil {
		img.Close()
		os.Remove(path)
		return nil, fmt.Errorf("inode block 0: %w", err)
	}
	for s := uint32(IListSector + 1); s < dataBlockStart; s++ {
		if err := img.WriteSector(s, zeroBuf); err != nil {
			img.Close()
			os.Remove(path)
			return nil, fmt.Errorf("inode block %d: %w", s, err)
		}
	}

	// Phase 4: Build free block chain
	if err := img.buildFreeBlockChain(dataBlockStart, totalBlocks); err != nil {
		img.Close()
		os.Remove(path)
		return nil, fmt.Errorf("free chain: %w", err)
	}

	// Phase 5: Create root directory
	if err := img.createRootDir(owner, group); err != nil {
		img.Close()
		os.Remove(path)
		return nil, fmt.Errorf("root dir: %w", err)
	}

	// Phase 6: Populate free inode cache in superblock
	// UFSD reads s_ninode/s_inode[] from the superblock at mount time.
	// Without this, UFSD sees 0 free inodes and rejects FOPEN.
	img.seedFreeInodeCache()

	// Phase 7: Write final superblock
	if err := img.writeSuperBlock(); err != nil {
		img.Close()
		os.Remove(path)
		return nil, fmt.Errorf("superblock: %w", err)
	}

	return img, nil
}

// Open opens an existing UFS370 disk image.
func Open(path string, readOnly bool) (*Image, error) {
	flag := os.O_RDWR
	if readOnly {
		flag = os.O_RDONLY
	}

	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	img := &Image{file: f, path: path, readOnly: readOnly}

	// Read and validate boot block
	bootBuf := make([]byte, 264) // 8 + 256
	if _, err := f.ReadAt(bootBuf, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("read boot block: %w", err)
	}

	img.boot.Type = be.Uint16(bootBuf[0:2])
	img.boot.Check = be.Uint16(bootBuf[2:4])
	img.boot.BlkSize = be.Uint16(bootBuf[4:6])

	if img.boot.Type != DiskTypeUFS {
		f.Close()
		return nil, fmt.Errorf("not a UFS disk (type=%d)", img.boot.Type)
	}
	if img.boot.Type+img.boot.Check != 0xFFFF {
		f.Close()
		return nil, fmt.Errorf("boot block checksum failed")
	}

	img.blkSize = uint32(img.boot.BlkSize)
	img.bootExt.Version = bootBuf[0x18]
	if img.bootExt.Version >= BootVersion1 {
		img.bootExt.CreateTime = be.Uint64(bootBuf[8:16])
		img.bootExt.UpdateTime = be.Uint64(bootBuf[16:24])
	}

	// Read superblock
	sbBuf := make([]byte, img.blkSize)
	if err := img.ReadSector(SuperBlockSector, sbBuf); err != nil {
		f.Close()
		return nil, fmt.Errorf("read superblock: %w", err)
	}

	img.sb.DataBlockStart = be.Uint32(sbBuf[0x00:])
	img.sb.VolumeSize = be.Uint32(sbBuf[0x04:])
	img.sb.LockFreeBlock = sbBuf[0x08]
	img.sb.LockFreeInode = sbBuf[0x09]
	img.sb.Modified = sbBuf[0x0A]
	img.sb.ReadOnly = sbBuf[0x0B]
	img.sb.UpdateTime = be.Uint32(sbBuf[0x0C:])
	img.sb.TotalFreeBlock = be.Uint32(sbBuf[0x10:])
	img.sb.TotalFreeInode = be.Uint32(sbBuf[0x14:])
	img.sb.CreateTime = be.Uint32(sbBuf[0x18:])
	img.sb.NFreeBlock = be.Uint32(sbBuf[0x1C:])
	for i := 0; i < MaxFreeBlock; i++ {
		img.sb.FreeBlock[i] = be.Uint32(sbBuf[0x20+i*4:])
	}
	img.sb.NFreeInode = be.Uint32(sbBuf[0xEC:])
	for i := 0; i < MaxFreeInode; i++ {
		img.sb.FreeInode[i] = be.Uint32(sbBuf[0xF0+i*4:])
	}
	img.sb.InodesPerBlock = be.Uint32(sbBuf[0x1F0:])
	img.sb.BlkSizeShift = be.Uint32(sbBuf[0x1F4:])
	img.sb.IListSector = be.Uint32(sbBuf[0x1F8:])

	return img, nil
}

// Close closes the image file.
func (img *Image) Close() error {
	if img.file != nil {
		return img.file.Close()
	}
	return nil
}

// Path returns the image file path.
func (img *Image) Path() string { return img.path }

// BlkSize returns the block size.
func (img *Image) BlkSize() uint32 { return img.blkSize }

// SB returns a copy of the superblock.
func (img *Image) SB() SuperBlock { return img.sb }

// Boot returns a copy of the boot block.
func (img *Image) Boot() BootBlock { return img.boot }

// BootExtension returns a copy of the boot extension.
func (img *Image) BootExtension() BootExt { return img.bootExt }

// ReadSector reads one sector (block) from the image.
func (img *Image) ReadSector(sector uint32, buf []byte) error {
	offset := int64(sector) * int64(img.blkSize)
	_, err := img.file.ReadAt(buf[:img.blkSize], offset)
	return err
}

// WriteSector writes one sector (block) to the image.
func (img *Image) WriteSector(sector uint32, buf []byte) error {
	if img.readOnly {
		return fmt.Errorf("image is read-only")
	}
	offset := int64(sector) * int64(img.blkSize)
	_, err := img.file.WriteAt(buf[:img.blkSize], offset)
	return err
}

// ReadInode reads an inode by number.
func (img *Image) ReadInode(ino uint32) (*DiskInode, error) {
	ipb := img.sb.InodesPerBlock
	sector := img.sb.IListSector + (ino-1)/ipb
	offset := ((ino - 1) % ipb) * InodeSize

	buf := make([]byte, img.blkSize)
	if err := img.ReadSector(sector, buf); err != nil {
		return nil, err
	}

	d := buf[offset : offset+InodeSize]
	di := &DiskInode{
		Mode:     be.Uint16(d[0x00:]),
		NLink:    be.Uint16(d[0x02:]),
		FileSize: be.Uint32(d[0x04:]),
		CodePage: be.Uint16(d[0x32:]),
	}
	copy(di.CTime.Raw[:], d[0x08:0x10])
	copy(di.MTime.Raw[:], d[0x10:0x18])
	copy(di.ATime.Raw[:], d[0x18:0x20])
	copy(di.Owner[:], d[0x20:0x29])
	copy(di.Group[:], d[0x29:0x32])
	for i := 0; i < NAddr; i++ {
		di.Addr[i] = be.Uint32(d[0x34+i*4:])
	}
	return di, nil
}

// ReadDir reads all non-deleted directory entries from a directory inode.
func (img *Image) ReadDir(dirIno uint32) ([]DirEntry, error) {
	di, err := img.ReadInode(dirIno)
	if err != nil {
		return nil, err
	}
	if di.Mode&IFMT != IFDIR {
		return nil, fmt.Errorf("inode %d is not a directory", dirIno)
	}

	var entries []DirEntry
	buf := make([]byte, img.blkSize)
	nde := img.blkSize / DirentSize
	nblocks := (di.FileSize + img.blkSize - 1) / img.blkSize

	for i := uint32(0); i < NAddrDirect && i < nblocks; i++ {
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
				continue
			}
			var de DirEntry
			de.InodeNumber = ino
			copy(de.Name[:], buf[off+4:off+64])
			entries = append(entries, de)
		}
	}
	return entries, nil
}

// NameString returns the filename from a DirEntry as a Go string (EBCDIC decoded).
func (de *DirEntry) NameString() string {
	return ebcdic.Decode(de.Name[:])
}

// --- internal helpers ---

func (img *Image) writeBootBlock() error {
	buf := make([]byte, img.blkSize)

	// Boot header
	be.PutUint16(buf[0:], DiskTypeUFS)
	be.PutUint16(buf[2:], ^uint16(DiskTypeUFS))
	be.PutUint16(buf[4:], uint16(img.blkSize))

	// Boot extension (version 1)
	now := TimeNow()
	nowMs := getU64BE(now.Raw[:])
	be.PutUint64(buf[8:], nowMs)   // create_time
	be.PutUint64(buf[16:], nowMs)  // update_time
	buf[0x18] = BootVersion1       // version

	img.boot = BootBlock{
		Type:    DiskTypeUFS,
		Check:   ^uint16(DiskTypeUFS),
		BlkSize: uint16(img.blkSize),
	}
	img.bootExt = BootExt{
		CreateTime: nowMs,
		UpdateTime: nowMs,
		Version:    BootVersion1,
	}

	return img.WriteSector(BootBlockSector, buf)
}

func (img *Image) writeSuperBlock() error {
	buf := make([]byte, img.blkSize)
	sb := &img.sb

	be.PutUint32(buf[0x000:], sb.DataBlockStart)
	be.PutUint32(buf[0x004:], sb.VolumeSize)
	buf[0x008] = sb.LockFreeBlock
	buf[0x009] = sb.LockFreeInode
	buf[0x00A] = sb.Modified
	buf[0x00B] = sb.ReadOnly
	be.PutUint32(buf[0x00C:], 0) // V1: timestamps in boot extension
	be.PutUint32(buf[0x010:], sb.TotalFreeBlock)
	be.PutUint32(buf[0x014:], sb.TotalFreeInode)
	be.PutUint32(buf[0x018:], 0) // V1
	be.PutUint32(buf[0x01C:], sb.NFreeBlock)
	for i := uint32(0); i < MaxFreeBlock; i++ {
		be.PutUint32(buf[0x020+i*4:], sb.FreeBlock[i])
	}
	be.PutUint32(buf[0x0EC:], sb.NFreeInode)
	for i := uint32(0); i < MaxFreeInode; i++ {
		be.PutUint32(buf[0x0F0+i*4:], sb.FreeInode[i])
	}
	be.PutUint32(buf[0x1F0:], sb.InodesPerBlock)
	be.PutUint32(buf[0x1F4:], sb.BlkSizeShift)
	be.PutUint32(buf[0x1F8:], sb.IListSector)

	return img.WriteSector(SuperBlockSector, buf)
}

func (img *Image) buildFreeBlockChain(start, total uint32) error {
	sb := &img.sb
	sb.NFreeBlock = 0

	// Fill superblock cache first
	block := start
	for block < total && sb.NFreeBlock < MaxFreeBlock {
		sb.FreeBlock[sb.NFreeBlock] = block
		sb.NFreeBlock++
		block++
	}

	// Build chain blocks for remaining free blocks
	buf := make([]byte, img.blkSize)
	var chain FreeBlockChain
	chainSector := start // first chain block

	for block < total {
		chain.FreeBlock[chain.NFreeBlock] = block
		chain.NFreeBlock++
		block++

		if chain.NFreeBlock == MaxFreeBlock || block >= total {
			// Write this chain block
			for i := range buf {
				buf[i] = 0
			}
			be.PutUint32(buf[0:], chain.NFreeBlock)
			for i := uint32(0); i < chain.NFreeBlock; i++ {
				be.PutUint32(buf[4+i*4:], chain.FreeBlock[i])
			}
			if err := img.WriteSector(chainSector, buf); err != nil {
				return err
			}
			if block < total {
				chainSector = chain.FreeBlock[0]
			}
			chain = FreeBlockChain{}
		}
	}

	return nil
}

func (img *Image) createRootDir(owner, group string) error {
	sb := &img.sb

	// Allocate inode 2 (root) from free inode cache
	// Seed free inode cache with root inode for allocation,
	// then clear it — subsequent AllocInode calls will scan the ilist.
	sb.NFreeInode = 1
	sb.FreeInode[0] = InodeRoot

	// Allocate one data block for root directory
	if sb.NFreeBlock == 0 {
		return fmt.Errorf("no free blocks for root directory")
	}
	sb.NFreeBlock--
	rootBlock := sb.FreeBlock[sb.NFreeBlock]
	sb.TotalFreeBlock--
	sb.TotalFreeInode--

	// Root inode consumed — clear the cache so AllocInode scans for inode 3+
	sb.NFreeInode = 0
	sb.FreeInode[0] = 0

	// Write root inode
	now := TimeNow()
	inodeBuf := make([]byte, img.blkSize)
	if err := img.ReadSector(IListSector, inodeBuf); err != nil {
		return err
	}

	// Inode 2 is at offset (2-1) % ipb * 128 = 128
	off := uint32(InodeSize) // inode 2 at index 1 in first inode block
	be.PutUint16(inodeBuf[off+0x00:], IFDIR|DefaultUmask)
	be.PutUint16(inodeBuf[off+0x02:], 2) // nlink: . and ..
	be.PutUint32(inodeBuf[off+0x04:], DirentSize*2) // filesize: 2 entries
	copy(inodeBuf[off+0x08:], now.Raw[:]) // ctime
	copy(inodeBuf[off+0x10:], now.Raw[:]) // mtime
	copy(inodeBuf[off+0x18:], now.Raw[:]) // atime
	copy(inodeBuf[off+0x20:], ebcdic.Encode(owner, 9))
	copy(inodeBuf[off+0x29:], ebcdic.Encode(group, 9))
	be.PutUint32(inodeBuf[off+0x34:], rootBlock) // addr[0]

	if err := img.WriteSector(IListSector, inodeBuf); err != nil {
		return err
	}

	// Write root directory data block (. and ..)
	dirBuf := make([]byte, img.blkSize)
	// entry 0: "."
	be.PutUint32(dirBuf[0:], InodeRoot)
	copy(dirBuf[4:], ebcdic.Encode(".", 60))
	// entry 1: ".."
	be.PutUint32(dirBuf[64:], InodeRoot) // root parent is itself
	copy(dirBuf[68:], ebcdic.Encode("..", 60))

	return img.WriteSector(rootBlock, dirBuf)
}

// seedFreeInodeCache scans the inode list and fills the superblock's
// free inode cache (s_ninode / s_inode[]). Must be called after
// createRootDir so that allocated inodes (0, 1, 2) are already marked.
func (img *Image) seedFreeInodeCache() {
	sb := &img.sb
	sb.NFreeInode = 0

	ipb := sb.InodesPerBlock
	buf := make([]byte, img.blkSize)

	for sector := sb.IListSector; sector < sb.DataBlockStart; sector++ {
		if err := img.ReadSector(sector, buf); err != nil {
			continue
		}
		for i := uint32(0); i < ipb; i++ {
			off := i * InodeSize
			mode := be.Uint16(buf[off:])
			// Reserved inodes 0,1 are filled with 0xFF, skip them
			if mode != 0 {
				continue
			}

			ino := (sector-sb.IListSector)*ipb + i + 1
			if ino <= InodeBALBLK {
				continue
			}

			sb.FreeInode[sb.NFreeInode] = ino
			sb.NFreeInode++
			if sb.NFreeInode >= MaxFreeInode {
				return
			}
		}
	}
}
