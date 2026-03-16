package ufs

import "fmt"

// AllocBlock allocates one free data block from the free block chain.
// Returns the sector number of the allocated block.
func (img *Image) AllocBlock() (uint32, error) {
	if img.readOnly {
		return 0, fmt.Errorf("image is read-only")
	}
	sb := &img.sb

	if sb.NFreeBlock == 0 {
		return 0, fmt.Errorf("no free blocks")
	}

	sb.NFreeBlock--
	block := sb.FreeBlock[sb.NFreeBlock]
	sb.FreeBlock[sb.NFreeBlock] = 0

	// If cache is now empty and the block we just took is a chain block,
	// reload the cache from it before returning it.
	if sb.NFreeBlock == 0 && sb.TotalFreeBlock > 1 {
		// The block at freeblock[0] might be a chain pointer.
		// Read it to see if it's a chain block.
		buf := make([]byte, img.blkSize)
		if err := img.ReadSector(block, buf); err == nil {
			nfb := be.Uint32(buf[0:4])
			if nfb > 0 && nfb <= MaxFreeBlock {
				// It's a chain block — reload cache
				sb.NFreeBlock = nfb
				for i := uint32(0); i < nfb; i++ {
					sb.FreeBlock[i] = be.Uint32(buf[4+i*4:])
				}
				// The block itself is now consumed as a chain block,
				// we need to allocate again from the refilled cache
				sb.NFreeBlock--
				block = sb.FreeBlock[sb.NFreeBlock]
				sb.FreeBlock[sb.NFreeBlock] = 0
			}
		}
	}

	sb.TotalFreeBlock--

	// Zero-fill the allocated block
	zeroBuf := make([]byte, img.blkSize)
	if err := img.WriteSector(block, zeroBuf); err != nil {
		return 0, fmt.Errorf("zero-fill block %d: %w", block, err)
	}

	return block, nil
}

// AllocInode allocates one free inode number from the free inode cache.
// If the cache is empty, scans the inode list for free inodes.
func (img *Image) AllocInode() (uint32, error) {
	if img.readOnly {
		return 0, fmt.Errorf("image is read-only")
	}
	sb := &img.sb

	// Try cache first
	if sb.NFreeInode > 0 {
		sb.NFreeInode--
		ino := sb.FreeInode[sb.NFreeInode]
		sb.FreeInode[sb.NFreeInode] = 0
		sb.TotalFreeInode--
		return ino, nil
	}

	// Cache empty — scan inode list for free inodes (mode == 0)
	ipb := sb.InodesPerBlock
	buf := make([]byte, img.blkSize)

	for sector := sb.IListSector; sector < sb.DataBlockStart; sector++ {
		if err := img.ReadSector(sector, buf); err != nil {
			continue
		}
		for i := uint32(0); i < ipb; i++ {
			off := i * InodeSize
			mode := be.Uint16(buf[off:])
			if mode != 0 {
				continue
			}

			// Calculate inode number
			ino := (sector-sb.IListSector)*ipb + i + 1
			if ino <= InodeBALBLK {
				continue // skip reserved inodes
			}

			// Found a free inode — also refill cache while we're here
			sb.TotalFreeInode--
			return ino, nil
		}
	}

	return 0, fmt.Errorf("no free inodes")
}

// FlushSuperBlock writes the current in-memory superblock to disk.
// Must be called after AllocBlock/AllocInode to persist changes.
func (img *Image) FlushSuperBlock() error {
	return img.writeSuperBlock()
}

// WriteInode writes a DiskInode to the image at the given inode number.
func (img *Image) WriteInode(ino uint32, di *DiskInode) error {
	if img.readOnly {
		return fmt.Errorf("image is read-only")
	}

	ipb := img.sb.InodesPerBlock
	sector := img.sb.IListSector + (ino-1)/ipb
	offset := ((ino - 1) % ipb) * InodeSize

	buf := make([]byte, img.blkSize)
	if err := img.ReadSector(sector, buf); err != nil {
		return fmt.Errorf("read inode sector %d: %w", sector, err)
	}

	d := buf[offset : offset+InodeSize]
	be.PutUint16(d[0x00:], di.Mode)
	be.PutUint16(d[0x02:], di.NLink)
	be.PutUint32(d[0x04:], di.FileSize)
	copy(d[0x08:0x10], di.CTime.Raw[:])
	copy(d[0x10:0x18], di.MTime.Raw[:])
	copy(d[0x18:0x20], di.ATime.Raw[:])
	copy(d[0x20:0x29], di.Owner[:])
	copy(d[0x29:0x32], di.Group[:])
	be.PutUint16(d[0x32:], di.CodePage)
	for i := 0; i < NAddr; i++ {
		be.PutUint32(d[0x34+i*4:], di.Addr[i])
	}

	return img.WriteSector(sector, buf)
}
