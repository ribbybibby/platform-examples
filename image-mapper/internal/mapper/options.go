package mapper

// Option configures a Mapper
type Option func(*options)

type options struct {
	ignoreFns []IgnoreFn
	orgName   string
}

// WithIgnoreFns is a functional option that configures the IgnoreFns used by
// the mapper
func WithIgnoreFns(ignoreFns ...IgnoreFn) Option {
	return func(o *options) {
		o.ignoreFns = ignoreFns
	}
}

// WithOrgName is a functional option that configures the name of the
// organization that we list repositories for
func WithOrgName(orgName string) Option {
	return func(o *options) {
		o.orgName = orgName
	}
}
