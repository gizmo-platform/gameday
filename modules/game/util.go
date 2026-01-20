package game

import (
	"fmt"
	"log/slog"
	"strings"

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

func exprForElementState(e GameElement, s GameElementState) string {
	switch strings.ToLower(e.Type) {
	case "count":
		return fmt.Sprintf("int(%s_%s * %d)", e.EID, s.SID, s.Each)
	case "boolean":
		return fmt.Sprintf("int(%s_%s > 0 ? %d : 0)", e.EID, s.SID, s.Each)
	case "radio":
		vals := []string{}
		for _, value := range s.Values {
			vals = append(vals, fmt.Sprintf("%d:%d", value.ID, value.Points))
		}
		eStr := fmt.Sprintf("{%s}", strings.Join(vals, ", "))
		return fmt.Sprintf("int(%s[string(%s_%s)])", eStr, e.EID, s.SID)
	default:
		return "0"
	}
}
