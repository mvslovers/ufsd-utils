// Package ufs implements the UFS370 on-disk filesystem format.
//
// All structures are big-endian (S/370 native byte order).
// All strings are EBCDIC on disk; this package converts to/from UTF-8.
package ufs

// Block size constraints
const (
	MinBlockSize = 512
	MaxBlockSize = 8192
	DefaultBlockSize = 4096
)

// Special sectors
const (
	BootBlockSector  = 0
	SuperBlockSector = 1
	IListSector      = 2
)

// Boot block type values
const (
	DiskTypeRaw     = 0
	DiskTypeUnknown = 1
	DiskTypeUFS     = 2
)

// Boot extension version
const (
	BootVersion0 = 0 // no extension
	BootVersion1 = 1 // has extension with time64 timestamps
)

// Special inode numbers
const (
	InodeReserved = 0 // never used
	InodeBALBLK   = 1 // monument, never used
	InodeRoot     = 2 // root directory (always)
)

// Inode mode flags (file type mask)
const (
	IFMT  = 0xF000 // file type mask
	IFIFO = 0x1000 // FIFO (named pipe, BSD)
	IFCHR = 0x2000 // character device
	IFDIR = 0x4000 // directory
	IFBLK = 0x6000 // block device
	IFREG = 0x8000 // regular file
	IFLNK = 0xA000 // symbolic link (BSD)
	IFSCK = 0xC000 // socket (BSD)
)

// Default permission mask
const DefaultUmask = 0755

// Address list sizes
const (
	NAddr       = 19 // total address slots in inode
	NAddrDirect = 16 // direct data block slots (addr[0..15])
	NAddrIndex1 = 16 // single indirect
	NAddrIndex2 = 17 // double indirect
	NAddrIndex3 = 18 // triple indirect
)

// Directory entry
const (
	NameMax     = 59  // max filename chars (excl. NUL)
	PathMax     = 256 // max path length
	DirentSize  = 64  // on-disk directory entry size
	InodeSize   = 128 // on-disk inode size
)

// Superblock free caches
const (
	MaxFreeBlock = 51 // free block cache size in superblock
	MaxFreeInode = 64 // free inode cache size in superblock
)

// BootBlock is the first 8 bytes of sector 0.
type BootBlock struct {
	Type    uint16 // filesystem type (2 = UFS)
	Check   uint16 // checksum: ~Type
	BlkSize uint16 // physical block size in bytes
	Padding uint16 // alignment
}

// BootExt is the boot block extension (256 bytes at offset 8 in sector 0).
// Present only on version 1 disks.
type BootExt struct {
	CreateTime uint64   // disk creation time (ms since epoch)
	UpdateTime uint64   // last update time (ms since epoch)
	Version    uint8    // format version (0 or 1)
	Unused1    [3]byte
	Unused2    uint32
	Unused3    [56]uint32
}

// SuperBlock occupies the first 512 bytes of sector 1.
type SuperBlock struct {
	DataBlockStart uint32          // first data block sector
	VolumeSize     uint32          // total sectors on disk
	LockFreeBlock  uint8           // free block lock (runtime)
	LockFreeInode  uint8           // free inode lock (runtime)
	Modified       uint8           // modified flag (runtime)
	ReadOnly       uint8           // read-only flag
	UpdateTime     uint32          // V0: seconds since epoch, V1: 0
	TotalFreeBlock uint32          // total free data blocks
	TotalFreeInode uint32          // total free inodes
	CreateTime     uint32          // V0: seconds since epoch, V1: 0
	NFreeBlock     uint32          // entries in FreeBlock[]
	FreeBlock      [51]uint32      // free block cache
	NFreeInode     uint32          // entries in FreeInode[]
	FreeInode      [64]uint32      // free inode cache
	InodesPerBlock uint32          // blksize / 128
	BlkSizeShift   uint32          // log2(blksize)
	IListSector    uint32          // always 2
	Unused3        uint32          // reserved
}

// TimeV is the dual-format timestamp (8 bytes).
// V1: seconds + microseconds (useconds < 1_000_000)
// V2: 64-bit milliseconds since epoch
type TimeV struct {
	Raw [8]byte
}

// DiskInode is the on-disk inode structure (128 bytes).
type DiskInode struct {
	Mode     uint16      // file type + permissions
	NLink    uint16      // link count
	FileSize uint32      // file size in bytes
	CTime    TimeV       // creation time
	MTime    TimeV       // modification time
	ATime    TimeV       // access time
	Owner    [9]byte     // owner user ID + NUL (EBCDIC)
	Group    [9]byte     // group name + NUL (EBCDIC)
	CodePage uint16      // code page (0 = default)
	Addr     [19]uint32  // block address list
}

// DirEntry is the on-disk directory entry (64 bytes).
type DirEntry struct {
	InodeNumber uint32   // inode number (0 = free/deleted)
	Name        [60]byte // filename + NUL (EBCDIC)
}

// FreeBlockChain is the on-disk free block chain block (208 bytes).
// Used for chained free block lists beyond the superblock cache.
type FreeBlockChain struct {
	NFreeBlock uint32
	FreeBlock  [51]uint32
}
