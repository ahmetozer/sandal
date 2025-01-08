package loop

// LoopInfo64 represents the loop device information structure
type LoopInfo64 struct {
	Device         uint64
	Inode          uint64
	RDeviceNumber  uint64
	Offset         uint64
	SizeLimit      uint64
	Number         uint32
	EncryptType    uint32
	EncryptKeySize uint32
	Flags          uint32
	FileName       [64]byte
	CryptName      [64]byte
	EncryptKey     [32]byte
	Init           [2]uint64
}
