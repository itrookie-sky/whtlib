package logx

type outputMode uint8

const (
	outputPlain outputMode = iota
	outputFormatted
	outputStructured
)

const archiveTimeFormat = "20060102T150405.000000000Z"
