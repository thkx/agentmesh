package policy

import "context"

type Decision struct {
	Allowed bool
	Reason  string
}
type Policy interface {
	AuthorizeNode(context.Context, string, string) (Decision, error)
}
type AllowAll struct{}

func (AllowAll) AuthorizeNode(context.Context, string, string) (Decision, error) {
	return Decision{Allowed: true}, nil
}
