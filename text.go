package main

import (
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Funções de texto, conversão e número que já vêm na linguagem.

const textMax = 1 << 20 // tamanho máximo de um texto criado por repeat/pad

func argInt(a []any, i, line int, fn string) int64 {
	n, ok := a[i].(int64)
	if !ok {
		fail(line, "'%s' espera Int no argumento %d, mas recebeu %s", fn, i+1, typeName(a[i]))
	}
	return n
}

func argNum(a []any, i, line int, fn string) float64 {
	f, ok := toFloat(a[i])
	if !ok {
		fail(line, "'%s' espera um número no argumento %d, mas recebeu %s", fn, i+1, typeName(a[i]))
	}
	return f
}

func strList(parts []string) *Array {
	arr := &Array{Items: make([]any, len(parts))}
	for i, p := range parts {
		arr.Items[i] = p
	}
	return arr
}

// accents: letras com acento e seu equivalente simples (para slug).
var accents = func() map[rune]string {
	m := map[rune]string{}
	for plain, group := range map[string]string{
		"a": "áàâãäåāăą", "c": "çćĉċč", "e": "éèêëēĕėęě", "i": "íìîïĩīĭįı", "n": "ñńņň",
		"o": "óòôõöøōŏő", "u": "úùûüũūŭůűų", "y": "ýÿŷ", "s": "śŝşš", "z": "źżž", "d": "ďđ", "l": "ĺļľł", "r": "ŕŗř", "t": "ţťŧ", "g": "ĝğġģ",
	} {
		for _, r := range group {
			m[r] = plain
			m[unicode.ToUpper(r)] = plain
		}
	}
	m['ß'], m['æ'], m['Æ'], m['œ'], m['Œ'] = "ss", "ae", "ae", "oe", "oe"
	return m
}()

func slugify(s string) string {
	var sb strings.Builder
	dash := true // evita hífen no começo
	for _, r := range s {
		if rep, ok := accents[r]; ok {
			sb.WriteString(rep)
			dash = false
			continue
		}
		r = unicode.ToLower(r)
		if r < 128 && (r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			sb.WriteRune(r)
			dash = false
		} else if !dash {
			sb.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(sb.String(), "-")
}

func sliceBounds(n int, a []any, line int, fn string) (int, int) {
	from := argInt(a, 1, line, fn)
	to := int64(n)
	if len(a) > 2 {
		to = argInt(a, 2, line, fn)
	}
	clamp := func(v int64) int {
		if v < 0 {
			return 0
		}
		if v > int64(n) {
			return n
		}
		return int(v)
	}
	f, t := clamp(from), clamp(to)
	if f > t {
		f = t
	}
	return f, t
}

func isNumText(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E') {
			return false
		}
	}
	return true
}

func init() {
	def := func(name string, args int, fn func(a []any, line int) any) {
		builtins[name] = builtin{args, func(in *Interp, env *Env, line int, a []any) any { return fn(a, line) }}
	}

	// ---- texto ----
	def("split", 2, func(a []any, line int) any {
		s, sep := argStr(a, 0, line, "split"), argStr(a, 1, line, "split")
		if sep == "" {
			fail(line, "split: o separador não pode ser vazio")
		}
		return strList(strings.Split(s, sep))
	})
	def("join", 2, func(a []any, line int) any {
		arr, ok := a[0].(*Array)
		if !ok {
			fail(line, "join espera uma lista (Arr), mas recebeu %s", typeName(a[0]))
		}
		parts := make([]string, len(arr.Items))
		for i, it := range arr.Items {
			if s, ok := it.(string); ok {
				parts[i] = s
			} else {
				parts[i] = format(it)
			}
		}
		return strings.Join(parts, argStr(a, 1, line, "join"))
	})
	def("trim", 1, func(a []any, line int) any { return strings.TrimSpace(argStr(a, 0, line, "trim")) })
	def("upper", 1, func(a []any, line int) any { return strings.ToUpper(argStr(a, 0, line, "upper")) })
	def("lower", 1, func(a []any, line int) any { return strings.ToLower(argStr(a, 0, line, "lower")) })
	def("replace", 3, func(a []any, line int) any {
		old := argStr(a, 1, line, "replace")
		if old == "" {
			fail(line, "replace: o texto a procurar não pode ser vazio")
		}
		out := strings.ReplaceAll(argStr(a, 0, line, "replace"), old, argStr(a, 2, line, "replace"))
		if len(out) > textMax*10 {
			fail(line, "replace: resultado grande demais")
		}
		return out
	})
	def("starts", 2, func(a []any, line int) any {
		return strings.HasPrefix(argStr(a, 0, line, "starts"), argStr(a, 1, line, "starts"))
	})
	def("ends", 2, func(a []any, line int) any {
		return strings.HasSuffix(argStr(a, 0, line, "ends"), argStr(a, 1, line, "ends"))
	})
	// contains:texto:pedaço  |  contains:lista:item
	def("contains", 2, func(a []any, line int) any {
		switch x := a[0].(type) {
		case string:
			return strings.Contains(x, argStr(a, 1, line, "contains"))
		case *Array:
			for _, it := range x.Items {
				if equal(it, a[1]) {
					return true
				}
			}
			return false
		}
		fail(line, "'contains' espera String ou Arr, mas recebeu %s", typeName(a[0]))
		return nil
	})
	// index:texto:pedaço -> posição (em letras) ou -1  |  index:lista:item
	def("index", 2, func(a []any, line int) any {
		switch x := a[0].(type) {
		case string:
			sub := argStr(a, 1, line, "index")
			i := strings.Index(x, sub)
			if i < 0 {
				return int64(-1)
			}
			return int64(len([]rune(x[:i])))
		case *Array:
			for i, it := range x.Items {
				if equal(it, a[1]) {
					return int64(i)
				}
			}
			return int64(-1)
		}
		fail(line, "'index' espera String ou Arr, mas recebeu %s", typeName(a[0]))
		return nil
	})
	// slice:texto:de:até  (o "até" não entra; sem ele vai até o fim; fora dos limites é ajustado)
	def("slice", -1, func(a []any, line int) any {
		if len(a) < 2 || len(a) > 3 {
			fail(line, "'slice' espera (texto ou lista, de) ou (texto ou lista, de, até)")
		}
		switch x := a[0].(type) {
		case string:
			rs := []rune(x)
			f, t := sliceBounds(len(rs), a, line, "slice")
			return string(rs[f:t])
		case *Array:
			f, t := sliceBounds(len(x.Items), a, line, "slice")
			return &Array{Items: append([]any(nil), x.Items[f:t]...)}
		}
		fail(line, "'slice' espera String ou Arr, mas recebeu %s", typeName(a[0]))
		return nil
	})
	def("repeat", 2, func(a []any, line int) any {
		s, n := argStr(a, 0, line, "repeat"), argInt(a, 1, line, "repeat")
		if n < 0 || (len(s) > 0 && n > int64(textMax/len(s))) {
			fail(line, "repeat: quantidade inválida ou resultado grande demais")
		}
		return strings.Repeat(s, int(n))
	})
	// padleft:texto:largura:preenchimento  (ex.: padleft:"7":2:"0" dá "07")
	pad := func(name string, left bool) {
		def(name, 3, func(a []any, line int) any {
			s, w, ch := argStr(a, 0, line, name), argInt(a, 1, line, name), []rune(argStr(a, 2, line, name))
			if len(ch) != 1 {
				fail(line, "%s: o preenchimento é uma letra só", name)
			}
			if w > textMax {
				fail(line, "%s: largura grande demais", name)
			}
			n := int(w) - len([]rune(s))
			if n <= 0 {
				return s
			}
			fill := strings.Repeat(string(ch), n)
			if left {
				return fill + s
			}
			return s + fill
		})
	}
	pad("padleft", true)
	pad("padright", false)
	// slug:"Olá, Mundo!" -> "ola-mundo"
	def("slug", 1, func(a []any, line int) any { return slugify(argStr(a, 0, line, "slug")) })
	def("urlencode", 1, func(a []any, line int) any { return url.QueryEscape(argStr(a, 0, line, "urlencode")) })
	def("urldecode", 1, func(a []any, line int) any {
		s, err := url.QueryUnescape(argStr(a, 0, line, "urldecode"))
		if err != nil {
			return nil
		}
		return s
	})

	// ---- conversão: devolvem nil se não der, para combinar com ?? ----
	def("int", 1, func(a []any, line int) any {
		switch x := a[0].(type) {
		case int64:
			return x
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 9e18 {
				return nil
			}
			return int64(x)
		case string:
			if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
				return n
			}
		}
		return nil
	})
	def("float", 1, func(a []any, line int) any {
		switch x := a[0].(type) {
		case int64:
			return float64(x)
		case float64:
			return x
		case string:
			x = strings.TrimSpace(x)
			if isNumText(x) {
				if f, err := strconv.ParseFloat(x, 64); err == nil && !math.IsInf(f, 0) {
					return f
				}
			}
		}
		return nil
	})

	// ---- números ----
	def("abs", 1, func(a []any, line int) any {
		if n, ok := a[0].(int64); ok {
			if n < 0 {
				return -n
			}
			return n
		}
		return math.Abs(argNum(a, 0, line, "abs"))
	})
	round := func(name string, f func(float64) float64) {
		def(name, 1, func(a []any, line int) any {
			if n, ok := a[0].(int64); ok {
				return n
			}
			r := f(argNum(a, 0, line, name))
			if math.IsNaN(r) || math.Abs(r) > 9e18 {
				fail(line, "%s: número fora do alcance", name)
			}
			return int64(r)
		})
	}
	round("floor", math.Floor)
	round("ceil", math.Ceil)
	// round:x -> Int mais próximo  |  round:x:2 -> Float com 2 casas
	def("round", -1, func(a []any, line int) any {
		if len(a) < 1 || len(a) > 2 {
			fail(line, "'round' espera (número) ou (número, casas)")
		}
		x := argNum(a, 0, line, "round")
		if len(a) == 1 {
			r := math.Round(x)
			if math.IsNaN(r) || math.Abs(r) > 9e18 {
				fail(line, "round: número fora do alcance")
			}
			return int64(r)
		}
		d := argInt(a, 1, line, "round")
		if d < 0 || d > 15 {
			fail(line, "round: as casas vão de 0 a 15")
		}
		p := math.Pow(10, float64(d))
		return math.Round(x*p) / p
	})
	minmax := func(name string, less bool) {
		def(name, 2, func(a []any, line int) any {
			x, y := argNum(a, 0, line, name), argNum(a, 1, line, name)
			pickFirst := x >= y
			if less {
				pickFirst = x <= y
			}
			if pickFirst {
				return a[0]
			}
			return a[1]
		})
	}
	minmax("min", true)
	minmax("max", false)
}
