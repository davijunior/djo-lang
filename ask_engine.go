//go:build !nodb

package main

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// O motor de consulta do /ask: um plano (tipo, filtros, ordem, limite) vira UM SELECT parametrizado.
//
// Regras que o motor garante, sejam quais forem as palavras que levaram até o plano:
//   - nomes de tabela e coluna vêm só do ask.djo; do plano só entram nomes de campo, e só se o nível os enxerga;
//   - valores vão sempre como parâmetros (?); o % e o _ de um LIKE são escapados;
//   - um campo que o nível não enxerga (negado, ou fora do "fields" da regra) responde igual a um que não existe,
//     em ler, filtrar, ordenar e contar;
//   - o "where" da regra do nível é somado a tudo (E) e nenhum filtro consegue afrouxá-lo;
//   - o número de linhas é limitado por max_results e o tempo por timeout_ms.

type askFilter struct {
	Field string
	Op    string
	Vals  []any // string, int64, float64, bool, Date ou DateTime, conforme a natureza do campo
}

type askOrder struct {
	Field string
	Desc  bool
}

type askPlan struct {
	Type      string
	Filters   []askFilter
	Order     *askOrder
	Fields    []string // nil = todos os visíveis
	Limit     int64    // 0 = padrão
	Page      int64    // 0 = primeira
	CountOnly bool     // só conta
	Total     bool     // além da página, conta tudo
}

type askResult struct {
	Items    []*Map
	Count    int64
	HasCount bool
	Page     int64
	Limit    int64
	More     bool
}

const askDefaultLimit = 20

func askQuote(kind, id string) string {
	if kind == kindMySQL {
		return "`" + id + "`"
	}
	return `"` + id + `"`
}

func askEscapeLike(s string) string {
	s = strings.ReplaceAll(s, "!", "!!")
	s = strings.ReplaceAll(s, "%", "!%")
	return strings.ReplaceAll(s, "_", "!_")
}

type askBuild struct {
	kind   string
	view   *askView
	t      *askType
	where  []string
	params []any
}

func (b *askBuild) col(f *askField) string { return "t0." + askQuote(b.kind, f.column) }

func (b *askBuild) add(cond string, params ...any) {
	b.where = append(b.where, cond)
	b.params = append(b.params, params...)
}

// dateParam prepara um valor de data para a coluna do campo (epoch vira Int).
func (b *askBuild) dateParam(f *askField, v any) any {
	if f.as == "epoch" {
		if dt, ok := v.(DateTime); ok {
			return dt.sec
		}
	}
	return dbDateFor(b.kind, v)
}

func askValText(v any, field string) (string, error) {
	s, ok := v.(string)
	if !ok || len(s) > 200 || strings.ContainsRune(s, 0) {
		return "", askFail("bad_value", "o valor de %q precisa ser um texto de até 200 caracteres", field)
	}
	return s, nil
}

// cond acrescenta a condição de um filtro do plano.
func (b *askBuild) cond(fl askFilter) error {
	if !b.view.has(fl.Field) {
		return askFail("unknown_field", "o tipo %q não tem o campo %q", b.t.name, fl.Field)
	}
	f := b.t.fields[fl.Field]
	col := b.col(f)
	need := func(n int) error {
		if len(fl.Vals) != n {
			return askFail("bad_value", "o filtro %q de %q espera %d valor(es)", fl.Op, fl.Field, n)
		}
		return nil
	}
	badOp := func() error {
		return askFail("bad_op", "o filtro %q não vale para o campo %q (%s)", fl.Op, fl.Field, f.kind)
	}
	switch f.kind {
	case "text":
		if err := need(1); err != nil {
			return err
		}
		s, err := askValText(fl.Vals[0], fl.Field)
		if err != nil {
			return err
		}
		switch fl.Op {
		case "eq":
			b.add(col+" = ?", s)
		case "contains":
			b.add("LOWER("+col+") LIKE ? ESCAPE '!'", "%"+askEscapeLike(strings.ToLower(s))+"%")
		case "starts":
			b.add("LOWER("+col+") LIKE ? ESCAPE '!'", askEscapeLike(strings.ToLower(s))+"%")
		default:
			return badOp()
		}
	case "number":
		for _, v := range fl.Vals {
			switch v.(type) {
			case int64, float64:
			default:
				return askFail("bad_value", "o valor de %q precisa ser um número", fl.Field)
			}
		}
		ops := map[string]string{"eq": "=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}
		if op, ok := ops[fl.Op]; ok {
			if err := need(1); err != nil {
				return err
			}
			b.add(col+" "+op+" ?", fl.Vals[0])
		} else if fl.Op == "between" {
			if err := need(2); err != nil {
				return err
			}
			b.add(col+" >= ? AND "+col+" <= ?", fl.Vals[0], fl.Vals[1])
		} else {
			return badOp()
		}
	case "bool":
		if fl.Op != "eq" {
			return badOp()
		}
		if err := need(1); err != nil {
			return err
		}
		v, ok := fl.Vals[0].(bool)
		if !ok {
			return askFail("bad_value", "o valor de %q precisa ser true ou false", fl.Field)
		}
		b.add(col+" = ?", v)
	case "enum":
		if fl.Op != "eq" && fl.Op != "in" {
			return badOp()
		}
		if len(fl.Vals) == 0 || (fl.Op == "eq" && len(fl.Vals) != 1) || len(fl.Vals) > 20 {
			return askFail("bad_value", "o filtro %q de %q espera um valor (in: até 20)", fl.Op, fl.Field)
		}
		ph := make([]string, len(fl.Vals))
		ps := make([]any, len(fl.Vals))
		for i, v := range fl.Vals {
			s, _ := v.(string)
			if !contains(f.values, s) {
				return askFail("bad_value", "valor inválido para %q; valem: %s", fl.Field, strings.Join(f.values, ", "))
			}
			ph[i], ps[i] = "?", s
		}
		b.add(col+" IN ("+strings.Join(ph, ", ")+")", ps...)
	case "tags":
		if fl.Op != "has" {
			return badOp()
		}
		if err := need(1); err != nil {
			return err
		}
		s, err := askValText(fl.Vals[0], fl.Field)
		if err != nil {
			return err
		}
		q := func(x string) string { return askQuote(b.kind, x) }
		b.add("EXISTS (SELECT 1 FROM "+q(f.from)+" AS g WHERE g."+q(f.key)+" = t0."+q(b.t.idCol)+" AND LOWER(g."+q(f.value)+") = ?)", strings.ToLower(s))
	case "date", "datetime":
		return b.dateCond(f, fl)
	}
	return nil
}

func (b *askBuild) dateCond(f *askField, fl askFilter) error {
	col := b.col(f)
	isDT := f.kind == "datetime"
	day := func(v any) (Date, error) {
		switch x := v.(type) {
		case Date:
			return x, nil
		case DateTime:
			if isDT {
				return Date{}, nil // tratado como instante, abaixo
			}
		}
		return Date{}, askFail("bad_value", "o valor de %q precisa ser uma data (AAAA-MM-DD)", f.name)
	}
	next := func(d Date) (Date, error) {
		if !dateInRange(d.days + 1) {
			return Date{}, askFail("bad_value", "data fora do intervalo em %q", f.name)
		}
		return Date{d.days + 1}, nil
	}
	// lower e upper dão o limite como (valor, incluso?). Uma Date num campo datetime cobre o dia inteiro.
	type bound struct {
		v   any
		inc bool
	}
	lower := func(v any) (bound, error) { // começo
		if _, err := day(v); err != nil {
			return bound{}, err
		}
		if d, ok := v.(Date); ok && isDT {
			return bound{DateTime{d.days * 86400}, true}, nil
		}
		return bound{v, true}, nil
	}
	upperOf := func(v any) (bound, error) { // fim, incluindo o dia/instante dado
		if _, err := day(v); err != nil {
			return bound{}, err
		}
		if d, ok := v.(Date); ok && isDT {
			n, err := next(d)
			if err != nil {
				return bound{}, err
			}
			return bound{DateTime{n.days * 86400}, false}, nil
		}
		return bound{v, true}, nil
	}
	put := func(lo, hi *bound) {
		var cs []string
		var ps []any
		if lo != nil {
			op := ">="
			if !lo.inc {
				op = ">"
			}
			cs, ps = append(cs, col+" "+op+" ?"), append(ps, b.dateParam(f, lo.v))
		}
		if hi != nil {
			op := "<="
			if !hi.inc {
				op = "<"
			}
			cs, ps = append(cs, col+" "+op+" ?"), append(ps, b.dateParam(f, hi.v))
		}
		b.add(strings.Join(cs, " AND "), ps...)
	}
	n := map[string]int{"eq": 1, "from": 1, "to": 1, "after": 1, "before": 1, "between": 2, "year": 1}
	want, ok := n[fl.Op]
	if !ok {
		return askFail("bad_op", "o filtro %q não vale para o campo %q (%s)", fl.Op, fl.Field, f.kind)
	}
	if len(fl.Vals) != want {
		return askFail("bad_value", "o filtro %q de %q espera %d valor(es)", fl.Op, fl.Field, want)
	}
	switch fl.Op {
	case "year":
		y, ok := fl.Vals[0].(int64)
		if !ok || y < 1 || y > 9999 {
			return askFail("bad_value", "o ano de %q precisa ser um número de 1 a 9999", f.name)
		}
		first, _ := makeDate(y, 1, 1)
		var lo, hi bound
		if isDT {
			lo = bound{DateTime{first.days * 86400}, true}
			if y == 9999 {
				return askFail("bad_value", "o ano 9999 não pode ser consultado")
			}
			nxt, _ := makeDate(y+1, 1, 1)
			hi = bound{DateTime{nxt.days * 86400}, false}
		} else {
			last, _ := makeDate(y, 12, 31)
			lo, hi = bound{first, true}, bound{last, true}
		}
		put(&lo, &hi)
	case "eq":
		lo, err := lower(fl.Vals[0])
		if err != nil {
			return err
		}
		hi, err := upperOf(fl.Vals[0])
		if err != nil {
			return err
		}
		put(&lo, &hi)
	case "from":
		lo, err := lower(fl.Vals[0])
		if err != nil {
			return err
		}
		put(&lo, nil)
	case "to":
		hi, err := upperOf(fl.Vals[0])
		if err != nil {
			return err
		}
		put(nil, &hi)
	case "after":
		hi, err := upperOf(fl.Vals[0])
		if err != nil {
			return err
		}
		lo := bound{hi.v, !hi.inc} // depois do fim do dia (ou do instante)
		if hi.inc {
			lo.inc = false
		}
		put(&lo, nil)
	case "before":
		lo, err := lower(fl.Vals[0])
		if err != nil {
			return err
		}
		hi := bound{lo.v, false}
		put(nil, &hi)
	case "between":
		lo, err := lower(fl.Vals[0])
		if err != nil {
			return err
		}
		hi, err := upperOf(fl.Vals[1])
		if err != nil {
			return err
		}
		put(&lo, &hi)
	}
	return nil
}

// policy acrescenta o "where" da regra do nível.
func (b *askBuild) policy() {
	for _, c := range b.view.where {
		f := b.t.fields[c.field]
		col := b.col(f)
		if len(c.vals) == 1 {
			b.add(col+" = ?", c.vals[0])
			continue
		}
		ph := make([]string, len(c.vals))
		for i := range ph {
			ph[i] = "?"
		}
		b.add(col+" IN ("+strings.Join(ph, ", ")+")", c.vals...)
	}
}

func (c *askConfig) run(r dbRunner, kind, level string, p askPlan) (*askResult, error) {
	view := c.view(level, p.Type)
	if view == nil {
		return nil, askFail("unknown_type", "não existe o tipo %q", p.Type)
	}
	t := view.typ
	b := &askBuild{kind: kind, view: view, t: t}
	b.policy()
	for _, fl := range p.Filters {
		if err := b.cond(fl); err != nil {
			return nil, err
		}
	}
	whereSQL := ""
	if len(b.where) > 0 {
		whereSQL = " WHERE (" + strings.Join(b.where, ") AND (") + ")"
	}
	from := " FROM " + askQuote(kind, t.table) + " AS t0"
	timeout := time.Duration(c.limits.timeoutMS) * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	count := func() (int64, error) {
		rows, err := r.QueryContext(ctx, askSQL(kind, "SELECT COUNT(*)"+from+whereSQL), b.params...)
		if err != nil {
			return 0, askDBErr(err)
		}
		defer rows.Close()
		var n int64
		if rows.Next() {
			if err := rows.Scan(&n); err != nil {
				return 0, askDBErr(err)
			}
		}
		return n, askDBErr(rows.Err())
	}
	res := &askResult{}
	if p.CountOnly {
		n, err := count()
		if err != nil {
			return nil, err
		}
		res.Count, res.HasCount = n, true
		return res, nil
	}

	fields := view.fields
	if p.Fields != nil {
		fields = nil
		for _, fn := range p.Fields {
			if !view.has(fn) {
				return nil, askFail("unknown_field", "o tipo %q não tem o campo %q", t.name, fn)
			}
			fields = append(fields, fn)
		}
		if len(fields) == 0 {
			return nil, askFail("bad_plan", "escolha ao menos um campo")
		}
	}
	var cols []string
	var colFields []*askField
	hasTags := false
	for _, fn := range fields {
		f := t.fields[fn]
		if f.kind == "tags" {
			hasTags = true
			continue
		}
		cols = append(cols, "t0."+askQuote(kind, f.column))
		colFields = append(colFields, f)
	}
	idPos := -1
	if hasTags {
		idPos = len(cols)
		cols = append(cols, "t0."+askQuote(kind, t.idCol))
	}
	if len(cols) == 0 {
		cols = append(cols, "t0."+askQuote(kind, t.idCol))
	}

	order := "t0." + askQuote(kind, t.idCol)
	if p.Order != nil {
		if !view.has(p.Order.Field) {
			return nil, askFail("unknown_field", "o tipo %q não tem o campo %q", t.name, p.Order.Field)
		}
		of := t.fields[p.Order.Field]
		if of.kind == "tags" {
			return nil, askFail("bad_op", "não dá para ordenar por %q (%s)", of.name, of.kind)
		}
		dir := " ASC"
		if p.Order.Desc {
			dir = " DESC"
		}
		order = "t0." + askQuote(kind, of.column) + dir + ", " + order
	}
	limit := p.Limit
	if limit <= 0 {
		limit = askDefaultLimit
	}
	if limit > int64(c.limits.maxResults) {
		limit = int64(c.limits.maxResults)
	}
	page := p.Page
	if page < 1 {
		page = 1
	}
	if page > pageLast {
		page = pageLast
	}
	q := "SELECT " + strings.Join(cols, ", ") + from + whereSQL + " ORDER BY " + order +
		" LIMIT " + strconv.FormatInt(limit+1, 10) + " OFFSET " + strconv.FormatInt((page-1)*limit, 10)
	rows, err := r.QueryContext(ctx, askSQL(kind, q), b.params...)
	if err != nil {
		return nil, askDBErr(err)
	}
	defer rows.Close()
	var ids []any
	rowIdx := map[any][]*Map{}
	for rows.Next() {
		if int64(len(res.Items)) >= limit {
			res.More = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, askDBErr(err)
		}
		m := newMap()
		ci := 0
		for _, fn := range fields {
			f := t.fields[fn]
			if f.kind == "tags" {
				m.set(fn, &Array{})
				continue
			}
			m.set(fn, askRead(f, dbValue(vals[ci])))
			ci++
		}
		if idPos >= 0 {
			id := dbValue(vals[idPos])
			ids = append(ids, id)
			rowIdx[id] = append(rowIdx[id], m)
		}
		res.Items = append(res.Items, m)
	}
	if err := rows.Err(); err != nil {
		return nil, askDBErr(err)
	}
	rows.Close()
	if hasTags && len(ids) > 0 {
		for _, fn := range fields {
			f := t.fields[fn]
			if f.kind != "tags" {
				continue
			}
			ph := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
			tq := "SELECT g." + askQuote(kind, f.key) + ", g." + askQuote(kind, f.value) + " FROM " + askQuote(kind, f.from) +
				" AS g WHERE g." + askQuote(kind, f.key) + " IN (" + ph + ") ORDER BY g." + askQuote(kind, f.value)
			trows, err := r.QueryContext(ctx, askSQL(kind, tq), ids...)
			if err != nil {
				return nil, askDBErr(err)
			}
			for trows.Next() {
				var k, v any
				if err := trows.Scan(&k, &v); err != nil {
					trows.Close()
					return nil, askDBErr(err)
				}
				for _, m := range rowIdx[dbValue(k)] {
					cur, _ := m.get(fn)
					arr := cur.(*Array)
					arr.Items = append(arr.Items, askRead(&askField{kind: "text"}, dbValue(v)))
				}
			}
			trows.Close()
		}
	}
	res.Page, res.Limit = page, limit
	if p.Total {
		n, err := count()
		if err != nil {
			return nil, err
		}
		res.Count, res.HasCount = n, true
	}
	return res, nil
}

func askSQL(kind, q string) string {
	if kind == kindPostgres {
		return pgPlaceholders(q)
	}
	return q
}

func askDBErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "context deadline exceeded") {
		return askFail("timeout", "a consulta demorou demais; use filtros mais estreitos")
	}
	return askErr{Kind: "db", Msg: "não foi possível consultar agora", Cause: err.Error()}
}

// askRead converte o que a coluna devolveu para o valor da djo, pela natureza do campo.
func askRead(f *askField, v any) any {
	if v == nil {
		return nil
	}
	switch f.kind {
	case "text", "enum":
		switch x := v.(type) {
		case string:
			return x
		case int64, float64:
			return format(x)
		}
		return nil
	case "number":
		switch x := v.(type) {
		case int64, float64:
			return x
		case string:
			if i, err := strconv.ParseInt(x, 10, 64); err == nil {
				return i
			}
			if fl, err := strconv.ParseFloat(x, 64); err == nil {
				return fl
			}
		}
		return nil
	case "bool":
		switch x := v.(type) {
		case bool:
			return x
		case int64:
			return x != 0
		case string:
			return x == "1" || strings.EqualFold(x, "true")
		}
		return nil
	case "date":
		switch x := v.(type) {
		case string:
			if len(x) >= 10 {
				if d, ok := parseDate(x[:10]); ok {
					return d
				}
			}
		case int64:
			if dtInRange(x) {
				return dateOf(time.Unix(x, 0))
			}
		}
		return nil
	case "datetime":
		switch x := v.(type) {
		case string:
			if t, ok := parseDateTime(x); ok {
				return t
			}
			if d, ok := parseDate(x); ok {
				return DateTime{d.days * 86400}
			}
		case int64:
			if dtInRange(x) {
				return DateTime{x}
			}
		}
		return nil
	}
	return v
}

var _ = sql.ErrNoRows
