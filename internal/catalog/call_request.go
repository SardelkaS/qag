package catalog

// CallRequest carries an executed request. Only original calls have Expected.
type CallRequest struct {
	ID             string       `json:"id"`
	Kind           string       `json:"kind"`
	Resource       string       `json:"resource"`
	TimeoutSeconds int          `json:"timeoutSeconds"`
	HTTP           *HTTPRequest `json:"http,omitempty"`
	GRPC           *GRPCRequest `json:"grpc,omitempty"`
	SQL            *SQLRequest  `json:"sql,omitempty"`
	Expected       *Expectation `json:"expected,omitempty"`
}

func RequestFromBlock(b Block, includeExpected bool) CallRequest {
	r := CallRequest{ID: b.ID, Kind: b.Kind, Resource: b.Resource, TimeoutSeconds: b.TimeoutSeconds, HTTP: b.HTTP, GRPC: b.GRPC, SQL: b.SQL}
	if includeExpected {
		e := b.Expected
		r.Expected = &e
	}
	return r
}
func (r CallRequest) Block() Block {
	b := Block{ID: r.ID, Kind: r.Kind, Resource: r.Resource, TimeoutSeconds: r.TimeoutSeconds, HTTP: r.HTTP, GRPC: r.GRPC, SQL: r.SQL}
	if r.Expected != nil {
		b.Expected = *r.Expected
	}
	return b
}
