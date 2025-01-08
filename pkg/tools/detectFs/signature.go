package detectfs

type FSSignature struct {
	offset uint64
	magic  []byte
	fstype string
}

// // Common filesystem signatures
// var fsSignatures = []FSSignature{
// 	{0x438, []byte{0x53, 0xEF}, "ext4"}, // ext2/3/4
// 	{0x10400, []byte{0x52, 0x56}, "cramfs"},
// 	{0x0, []byte{0xEB, 0x3C, 0x90}, "vfat"}, // FAT32
// 	{0x0, []byte{0x38, 0x61, 0x19, 0x84}, "minix"},
// 	{0x1020, []byte{0x53, 0xEF}, "ext2"},
// }


// Common filesystem signatures
var fsSignatures = []FSSignature{
    // Linux filesystems
    {0x438, []byte{0x53, 0xEF}, "ext4"},      // ext2/3/4
    {0x0, []byte("hsqs"), "squashfs"},         // squashfs
    {0x0, []byte("QFSI"), "squashfs"},         // squashfs (alternative magic)
    {0x0, []byte{0x5D, 0x23, 0x40, 0x19}, "squashfs4"}, // squashfs4

    // FAT filesystems
    {0x0, []byte{0xEB, 0x3C, 0x90}, "vfat"},  // FAT32
    {0x52, []byte("FAT32"), "vfat"},          // FAT32 alternative signature
    {0x36, []byte("FAT"), "vfat"},            // FAT12/16
    
    // Other Linux/Unix filesystems
    {0x10400, []byte{0x52, 0x56}, "cramfs"},
    {0x0, []byte{0x38, 0x61, 0x19, 0x84}, "minix"},
    {0x0, []byte("BTRFS"), "btrfs"},          // btrfs
    {0x0, []byte{0x58, 0x46, 0x53, 0x42}, "xfs"}, // XFS
    {0x0, []byte{0x13, 0x13, 0x13, 0x13}, "nilfs2"}, // NILFS2
    {0x0, []byte("_BHRfS_M"), "btrfs"},       // BTRFS alternative magic
    
    // CD-ROM filesystems
    {0x8001, []byte{0x43, 0x44, 0x30, 0x30, 0x31}, "iso9660"}, // ISO9660
    {0x0, []byte{0x5F, 0x43, 0x44, 0x30, 0x30, 0x31}, "udf"}, // UDF
    
    // Network filesystems (for locally mounted images)
    {0x0, []byte{0x47, 0x58, 0x46, 0x53}, "gfs2"}, // GFS2
    {0x0, []byte{0x01, 0x02, 0x03, 0x04}, "ocfs2"}, // OCFS2
    
    // Other common filesystems
    {0x0, []byte{0x19, 0x01, 0x19, 0x01}, "swap"},  // SWAP
    {0x0, []byte("ZFS"), "zfs"},                    // ZFS
    {0x0, []byte{0x44, 0x44, 0x44, 0x44}, "dos"}, // DOS partition
    
    // Encrypted filesystems
    {0x0, []byte("LUKS"), "luks"},                  // LUKS encrypted
    {0x0, []byte{0xc1, 0x23, 0x45, 0x67}, "ecryptfs"}, // eCryptfs
    
    // Special filesystems
    {0x0, []byte("TMPFS"), "tmpfs"},               // TMPFS
    {0x0, []byte("OVERLAYFS"), "overlayfs"},       // OverlayFS
    
    // Compressed filesystems
    {0x0, []byte{0x1f, 0x8b}, "gzip"},            // gzip compressed
    {0x0, []byte{0x42, 0x5a, 0x68}, "bzip2"},     // bzip2 compressed
    
    // Forensics/backup filesystems
    {0x0, []byte("EXT-X"), "exfat"},              // exFAT
    {0x0, []byte("NTFS"), "ntfs"},                // NTFS
    
    // Legacy filesystems
    {0x1020, []byte{0x53, 0xEF}, "ext2"},         // explicit ext2
    {0x0, []byte{0x69, 0x69}, "minix1"},          // MINIX1
    {0x0, []byte{0x2D, 0x68, 0x65, 0x61}, "hfs"}, // HFS
}