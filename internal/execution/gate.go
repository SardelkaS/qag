package execution

import "context"

type requestGateKey struct{}

func WithRequestGate(ctx context.Context, gate func(context.Context) error) context.Context {
	return context.WithValue(ctx, requestGateKey{}, gate)
}
func checkRequestGate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if gate, ok := ctx.Value(requestGateKey{}).(func(context.Context) error); ok {
		return gate(ctx)
	}
	return nil
}
