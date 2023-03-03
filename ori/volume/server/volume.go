package server

type CephVolume struct {
	Requested Volume

	//Persisted in ceph
	ImageId   string
	ImagePool string
}

type Volume struct {
	Name string

	Bytes uint64
	IOPS  int64
	TPS   int64

	Image *Image
}
type Image struct {
	Name  string
	Bytes uint64
}
