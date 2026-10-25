package comparison

type Difference struct {
	Path     string `json:"path"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

type Result struct {
	Status               string       `json:"status"`
	Reason               string       `json:"reason,omitempty"`
	Differences          []Difference `json:"differences"`
	DifferencesTruncated bool         `json:"differencesTruncated"`
}
