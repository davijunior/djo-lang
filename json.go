package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Namespaces nativos do core da linguagem: Json.encode:x, Json.decode:s ...
// Os métodos usam o mesmo formato dos builtins.
var natives = map[string]map[string]builtin{}

// nativeNS devolve o namespace nativo, criando-o se for o primeiro a usá-lo
// (vários arquivos, como http.go e o cliente, acrescentam funções ao Http).
func nativeNS(name string) map[string]builtin {
	ns, ok := natives[name]
	if !ok {
		ns = map[string]builtin{}
		natives[name] = ns
	}
	return ns
}

const jsonMaxDepth = 100

func init() {
	natives["Json"] = map[string]builtin{
		"encode": {1, func(in *Interp, env *Env, line int, a []any) any {
			return jsonEncode(a[0], "", line)
		}},
		"pretty": {1, func(in *Interp, env *Env, line int, a []any) any {
			return jsonEncode(a[0], "  ", line)
		}},
		"decode": {1, func(in *Interp, env *Env, line int, a []any) any {
			s, ok := a[0].(string)
			if !ok {
				fail(line, "Json.decode espera String, mas recebeu %s", typeName(a[0]))
			}
			v, err := jsonDecode(s)
			if err != nil {
				return nil // texto inválido dá nil; use Json.valid: para distinguir de "null"
			}
			return v
		}},
		"valid": {1, func(in *Interp, env *Env, line int, a []any) any {
			s, ok := a[0].(string)
			if !ok {
				return false
			}
			_, err := jsonDecode(s)
			return err == nil
		}},
	}
}

func jsonEncode(v any, indent string, line int) string {
	var sb strings.Builder
	writeJSON(&sb, v, indent, 0, line)
	return sb.String()
}

func writeJSON(sb *strings.Builder, v any, indent string, depth, line int) {
	if depth > jsonMaxDepth {
		fail(line, "Json.encode: estrutura funda demais (ou com ciclo)")
	}
	nl := func(d int) {
		if indent != "" {
			sb.WriteByte('\n')
			sb.WriteString(strings.Repeat(indent, d))
		}
	}
	switch x := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		sb.WriteString(strconv.FormatBool(x))
	case int64:
		sb.WriteString(strconv.FormatInt(x, 10))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			fail(line, "Json.encode: número inválido (%v)", x)
		}
		sb.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
	case string:
		b, _ := json.Marshal(x)
		sb.Write(b)
	case *Array:
		if len(x.Items) == 0 {
			sb.WriteString("[]")
			return
		}
		sb.WriteByte('[')
		for i, it := range x.Items {
			if i > 0 {
				sb.WriteByte(',')
			}
			nl(depth + 1)
			writeJSON(sb, it, indent, depth+1, line)
		}
		nl(depth)
		sb.WriteByte(']')
	case *Map:
		keys := make([]string, len(x.Keys))
		for i, k := range x.Keys {
			keys[i] = fmt.Sprint(k)
		}
		writeObject(sb, keys, x.Vals, indent, depth, line)
	case *Instance:
		keys := make([]string, len(x.Def.Fields))
		for i, f := range x.Def.Fields {
			keys[i] = f.Name
		}
		writeObject(sb, keys, x.Vals, indent, depth, line)
	default:
		fail(line, "Json.encode: não sei converter %s", typeName(v))
	}
}

func writeObject(sb *strings.Builder, keys []string, vals []any, indent string, depth, line int) {
	if len(keys) == 0 {
		sb.WriteString("{}")
		return
	}
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		if indent != "" {
			sb.WriteByte('\n')
			sb.WriteString(strings.Repeat(indent, depth+1))
		}
		kb, _ := json.Marshal(k)
		sb.Write(kb)
		sb.WriteByte(':')
		if indent != "" {
			sb.WriteByte(' ')
		}
		writeJSON(sb, vals[i], indent, depth+1, line)
	}
	if indent != "" {
		sb.WriteByte('\n')
		sb.WriteString(strings.Repeat(indent, depth))
	}
	sb.WriteByte('}')
}

// jsonDecode converte JSON em valores da djo: objeto -> Map, lista -> Arr,
// inteiro -> Int, decimal -> Float. A ordem das chaves é mantida.
func jsonDecode(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	v, err := decodeValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("texto depois do JSON")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder, depth int) (any, error) {
	if depth > jsonMaxDepth {
		return nil, fmt.Errorf("fundo demais")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			arr := &Array{}
			for dec.More() {
				v, err := decodeValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				arr.Items = append(arr.Items, v)
			}
			_, err := dec.Token()
			return arr, err
		}
		m := newMap()
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			m.set(k.(string), v)
		}
		_, err := dec.Token()
		return m, err
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i, nil
		}
		f, err := t.Float64()
		return f, err
	}
	return tok, nil // string, bool, nil
}
