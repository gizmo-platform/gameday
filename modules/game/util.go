package game

import (
	"log/slog"

	"github.com/flosch/pongo2/v6"
)

// filterGetValueByKey gives functionality that really should have
// been in the template library to begin with and allows retrieving a
// single key from a map inside the template context.
func filterGetValueByKey(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	m, ok := in.Interface().(map[string]int)
	if !ok {
		slog.Warn("Tried to convert something that isn't a map", "something", in)
		return pongo2.AsValue(""), nil
	}
	return pongo2.AsValue(m[param.String()]), nil
}
