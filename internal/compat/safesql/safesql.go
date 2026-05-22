package safesql

type TrustedSQLString struct {
	s string
}

func New(s string) TrustedSQLString {
	return TrustedSQLString{s: s}
}

func EmptyString() TrustedSQLString {
	return TrustedSQLString{s: ""}
}

func (t TrustedSQLString) String() string {
	return t.s
}
