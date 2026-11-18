package main

import "fmt"

// djoError é o erro de sintaxe ou de execução, sempre com a linha.
type djoError struct {
	Line int
	Msg  string
	// campos do promise/reject (zero para o erro comum)
	Code  string // "" vale "erro"; o reject: pode dar outro
	Data  any    // dados do reject:
	File  string // "boosters/nome" quando o erro nasceu dentro de um booster
	Fatal bool   // limites de proteção (passos, tempo, chamadas, recursão): o promise não captura
}

func (e *djoError) Error() string {
	if e.Line == 0 {
		return "erro: " + e.Msg
	}
	return fmt.Sprintf("erro (linha %d): %s", e.Line, e.Msg)
}

func fail(line int, format string, a ...any) {
	panic(&djoError{Line: line, Msg: fmt.Sprintf(format, a...)})
}

// failFatal é o fail dos limites que protegem o servidor: o promise não os captura.
func failFatal(line int, format string, a ...any) {
	panic(&djoError{Line: line, Msg: fmt.Sprintf(format, a...), Fatal: true})
}

type Expr interface{}
type Stmt interface{}

// Array é a lista da djo (arr); tem semântica de referência.
type Array struct{ Items []any }

// Expressões
type (
	IntLit   struct{ Val int64 }
	FloatLit struct{ Val float64 }
	StrLit   struct{ Val string }
	BoolLit  struct{ Val bool }
	NilLit   struct{}
	Ident    struct {
		Name string
		Line int
	}
	ArrayLit struct{ Elems []Expr }
	// MapLit: {chave: valor, ...}
	MapLit struct {
		Keys, Vals []Expr
		Line       int
	}
	Index struct {
		X, Idx Expr
		Line   int
	}
	// Member: obj.campo
	Member struct {
		X    Expr
		Name string
		Line int
	}
	// MethodCall: obj.metodo:arg1:arg2
	MethodCall struct {
		X    Expr
		Name string
		Args []Expr
		Line int
	}
	// Placeholder: '.' como argumento = "use o valor padrão"
	Placeholder struct{ Line int }
	// ValLit: um valor já pronto (o servidor chamando uma action)
	ValLit struct{ V any }
	// Logical: and / or (com curto-circuito)
	Logical struct {
		Op   Kind
		L, R Expr
		Line int
	}
	// Pick: pick[cond ; a ; b]
	Pick struct {
		Cond, A, B Expr
		Line       int
	}
	// Input: input  |  input "pergunta"
	Input struct{ Prompt Expr }
	Unary struct {
		Op   Kind
		X    Expr
		Line int
	}
	Binary struct {
		Op   Kind
		L, R Expr
		Line int
	}
	// Call: nome:arg1:arg2
	Call struct {
		Name string
		Args []Expr
		Line int
	}
)

// Instruções
type (
	VarStmt struct {
		Name  string
		Init  Expr
		IsArr bool // 'arr': o valor precisa ser uma lista
		IsMap bool // 'map': o valor precisa ser um mapa
		Line  int
	}
	// AssignStmt: nome = v   ou   lista[i] = v (Target)
	AssignStmt struct {
		Name   string
		Target *Index
		Member *Member
		Value  Expr
		Line   int
	}
	// IncStmt: nome++ (Delta 1) ou nome-- (Delta -1)
	IncStmt struct {
		Name   string
		Member *Member
		Delta  int64
		Line   int
	}
	PrintStmt struct{ Value Expr }
	// EchoStmt: texto ou {{ expr }} de um template; Raw = sem escape de HTML.
	EchoStmt struct {
		Value Expr
		Raw   bool
	}
	ReturnStmt struct{ Value Expr }
	ExprStmt   struct{ X Expr }
	Param      struct{ Name, Type string }
	FuncDecl   struct {
		Name    string
		Params  []Param
		Body    []Stmt
		Line    int
		Owner   *StructDecl // struct que declarou o método
		Action  bool        // declarada com "action": também vira endpoint de agente
		Doc     string      // texto de descrição (primeira linha da action, entre aspas)
		Booster *booster    // booster que declarou a função (nil no core e nas páginas)
	}
	// FieldDecl: nome[Tipo] nocall = padrão
	FieldDecl struct {
		Name    string
		Type    string
		NoCall  bool // fora dos argumentos do construtor (ex.: id)
		Embed   bool // composição: a struct inclui outra, e herda campos e métodos
		Default Expr
	}
	StructDecl struct {
		Name     string
		Fields   []FieldDecl
		Methods  []*FuncDecl
		Parent   string      // struct sonof Pai
		ParentSD *StructDecl // preenchido por link
		own      []FieldDecl // campos declarados aqui (sem os do pai)
		linked   bool
		in       *Interp // o interpretador dono (para checar interfaces)
		linking  bool
		Line     int
		fieldIdx map[string]int
		methods  map[string]*FuncDecl
	}
	IfBranch struct {
		Cond Expr
		Body []Stmt
	}
	// IfStmt: if / elif... / else
	IfStmt struct {
		Branches []IfBranch
		Else     []Stmt
		HasElse  bool
		Line     int
	}
	MatchCase struct {
		Vals []Expr
		Body []Stmt
	}
	// MatchStmt: val[x] case[a, b] ... else
	MatchStmt struct {
		Val     Expr
		Cases   []MatchCase
		Else    []Stmt
		HasElse bool
		Line    int
	}
	// WhileStmt: repete enquanto Cond for verdadeira; Init roda uma vez.
	WhileStmt struct {
		Init []Stmt
		Cond Expr
		Body []Stmt
		Line int
	}
	// InterfaceDecl: os métodos que uma struct precisa ter (sem corpo)
	InterfaceDecl struct {
		Name    string
		Methods []*FuncDecl
		Line    int
	}
	// ForStmt: for{ each[x in lista] ... }  /  each[k, v in mapa]
	ForStmt struct {
		Vars []string
		Iter Expr
		Body []Stmt
		Line int
	}
	// UntilStmt: until(init ; cond ; step): repete até Cond ficar verdadeira.
	UntilStmt struct {
		Init Stmt
		Cond Expr
		Step Stmt
		Body []Stmt
		Line int
	}
)
