package cmpopts

import (
	"golang/cmp/cmp"

	"github.com/google/go-cmp/cmp/cmpopts"
)

func IgnoreFields(typ interface{}, names ...string) cmp.Option {
	return cmpopts.IgnoreFields(typ, names...)
}

func IgnoreUnexported(typs ...interface{}) cmp.Option {
	return cmpopts.IgnoreUnexported(typs...)
}

func SortSlices(less interface{}) cmp.Option {
	return cmpopts.SortSlices(less)
}

func EquateEmpty() cmp.Option {
	return cmpopts.EquateEmpty()
}
