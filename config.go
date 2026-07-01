package lidapters

type Config struct {
	AdapterID      string
	Protocol       string
	V2Scalar       string
	V2WasmHashes   map[string]struct{}
	AllowUnknownV2 bool
}

func DefaultConfig() Config {
	return Config{
		AdapterID: "blend",
		Protocol:  "blend",
		V2Scalar:  "1000000000000",
	}
}
