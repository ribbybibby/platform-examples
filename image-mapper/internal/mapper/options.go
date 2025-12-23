package mapper

// Option configures a Mapper
type Option func(*options)

type options struct {
	ignoreFns []IgnoreFn
	repo      string
}

// WithIgnoreFns is a functional option that configures the IgnoreFns used by
// the mapper
func WithIgnoreFns(ignoreFns ...IgnoreFn) Option {
	return func(o *options) {
		o.ignoreFns = ignoreFns
	}
}

// WithRepository is a functional option that configures the repository prefix
// of the returned results
func WithRepository(repo string) Option {
	return func(o *options) {
		o.repo = repo
	}
}
