package main

import (
	"reflect"
	"testing"
)

func TestSqlTopWords(t *testing.T) {
	cases := map[string][]string{
		"SELECT * FROM t ORDER BY id":                         {"select", "from", "t", "order", "by", "id"},
		"select 'limit' from t -- limit 1\n order by x":       {"select", "from", "t", "order", "by", "x"},
		"select * from (select * from t limit 5) order by id": {"select", "from", "order", "by", "id"},
		"select \"order\" /* limit */ from t":                 {"select", "from", "t"},
		"select 'it''s limit' from t limit 3":                 {"select", "from", "t", "limit"},
	}
	for sql, want := range cases {
		if got := sqlTopWords(sql); !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v, queria %v", sql, got, want)
		}
	}
}
