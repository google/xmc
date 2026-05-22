package flag

import (
	"flag"
	"time"
)

type Flag = flag.Flag
type FlagSet = flag.FlagSet
type Getter = flag.Getter
type Value = flag.Value

var (
	CommandLine = flag.CommandLine
	ErrHelp     = flag.ErrHelp
	Usage       = flag.Usage
)

func Arg(i int) string { return flag.Arg(i) }
func Args() []string   { return flag.Args() }
func NArg() int        { return flag.NArg() }
func NFlag() int       { return flag.NFlag() }
func Parse()           { flag.Parse() }
func Parsed() bool     { return flag.Parsed() }
func PrintDefaults()   { flag.PrintDefaults() }
func Visit(fn func(*Flag)) { flag.Visit(fn) }
func VisitAll(fn func(*Flag)) { flag.VisitAll(fn) }

func Bool(name string, value bool, usage string) *bool {
	return flag.Bool(name, value, usage)
}
func BoolVar(p *bool, name string, value bool, usage string) {
	flag.BoolVar(p, name, value, usage)
}
func Duration(name string, value time.Duration, usage string) *time.Duration {
	return flag.Duration(name, value, usage)
}
func DurationVar(p *time.Duration, name string, value time.Duration, usage string) {
	flag.DurationVar(p, name, value, usage)
}
// We can use generic wrappers or simply delegate:
func Float64(name string, value float64, usage string) *float64 {
	return flag.Float64(name, value, usage)
}
func Float64Var(p *float64, name string, value float64, usage string) {
	flag.Float64Var(p, name, value, usage)
}
func Int(name string, value int, usage string) *int {
	return flag.Int(name, value, usage)
}
func IntVar(p *int, name string, value int, usage string) {
	flag.IntVar(p, name, value, usage)
}
func Int64(name string, value int64, usage string) *int64 {
	return flag.Int64(name, value, usage)
}
func Int64Var(p *int64, name string, value int64, usage string) {
	flag.Int64Var(p, name, value, usage)
}
func String(name string, value string, usage string) *string {
	return flag.String(name, value, usage)
}
func StringVar(p *string, name string, value string, usage string) {
	flag.StringVar(p, name, value, usage)
}
func Uint(name string, value uint, usage string) *uint {
	return flag.Uint(name, value, usage)
}
func UintVar(p *uint, name string, value uint, usage string) {
	flag.UintVar(p, name, value, usage)
}
func Uint64(name string, value uint64, usage string) *uint64 {
	return flag.Uint64(name, value, usage)
}
func Uint64Var(p *uint64, name string, value uint64, usage string) {
	flag.Uint64Var(p, name, value, usage)
}
func Var(value Value, name string, usage string) {
	flag.Var(value, name, usage)
}
