package privacy

const (
	LenPointBytesCompressed      = 33
	pointCompressed         byte = 0x2
	SK                           = byte(0x00)
	VALUE                        = byte(0x01)
	SND                          = byte(0x02)
	RAND                         = byte(0x03)
	FULL                         = byte(0x04)

	CMRingSize 					= 8 // 2^3
	CMRingSizeExp			 	= 3
	LenCmIndexBytes 		= 10
)
