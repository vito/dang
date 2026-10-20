package dang

import (
	"sort"
	"strings"
	"testing"
)

// builtinsCallingFunctions lists every builtin that receives a function (a
// block or a function-typed argument). The effect checks assume builtins only
// call the functions they are given, during the call, and never store them
// (paramAcceptsFn in effects.go): an impure closure passed to one is not an
// escape, and a block passed to one may use non-local `return`/`break`.
//
// Adding a builtin that takes a function fails TestBuiltinsNeverStoreFunctions
// until it is listed here. Before listing it, make sure it calls the function
// only during the call, sequentially (an impure block run concurrently would
// race, like in a `{{ }}` field), and does not keep it past the call (in a
// value, a lazy result, or a goroutine). If it must do either, its parameter
// needs an effect variable that requires a pure argument instead.
var builtinsCallingFunctions = []string{
	"List.all",
	"List.any",
	"List.dropWhile",
	"List.each",
	"List.filter",
	"List.find",
	"List.map",
	"List.reduce",
	"List.reject",
	"List.takeWhile",
	"Map.each",
	"Map.map",
	"Map.reduce",
	"String.rewriteMatches",
	"assert",
	"loop",
}

func TestBuiltinsNeverStoreFunctions(t *testing.T) {
	var found []string
	visit := func(owner string) func(BuiltinDef) {
		return func(def BuiltinDef) {
			takesFn := def.BlockType != nil
			for _, p := range def.ParamTypes {
				if fnTypeOf(p.Type) != nil {
					takesFn = true
				}
			}
			if takesFn {
				found = append(found, owner+def.Name)
			}
		}
	}
	ForEachFunction(visit(""))
	for _, recv := range MethodReceivers() {
		ForEachMethod(recv, visit(recv.Named+"."))
	}
	for _, host := range StaticModules() {
		ForEachStaticMethod(host, visit(host.Named+"::"))
	}
	sort.Strings(found)
	want := append([]string(nil), builtinsCallingFunctions...)
	sort.Strings(want)
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Fatalf("builtins taking functions changed; review that none store them, then update builtinsCallingFunctions:\n%s",
			"\""+strings.Join(found, "\",\n\"")+"\",")
	}
}
