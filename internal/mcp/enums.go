package mcp

// Mode is the closed set of result modes the search tool accepts.
type Mode string

// Mode values understood by the search tool.
const (
	ModeLocate Mode = "locate"
	ModeFull   Mode = "full"
)

// Valid reports whether m is a known Mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeLocate, ModeFull:
		return true
	}
	return false
}

// Scope is the closed set of result scopes shared by every MCP tool
// that accepts a `scope` argument.
type Scope string

// Scope values shared across every MCP tool that filters by test/impl.
const (
	ScopeImpl Scope = "impl"
	ScopeTest Scope = "test"
	ScopeAll  Scope = "all"
)

// Valid reports whether s is a known Scope.
func (s Scope) Valid() bool {
	switch s {
	case ScopeImpl, ScopeTest, ScopeAll:
		return true
	}
	return false
}

// AllScopes lists every Scope value, in stable order. Used to populate
// MCP tool enum metadata so the description and validator can never
// drift out of sync.
func AllScopes() []string {
	return []string{string(ScopeImpl), string(ScopeTest), string(ScopeAll)}
}

// Direction is the closed set of dependency-traversal directions.
type Direction string

// Direction values understood by the dependencies tool.
const (
	DirectionCallers Direction = "callers"
	DirectionCallees Direction = "callees"
	DirectionBoth    Direction = "both"
)

// Valid reports whether d is a known Direction.
func (d Direction) Valid() bool {
	switch d {
	case DirectionCallers, DirectionCallees, DirectionBoth:
		return true
	}
	return false
}

// AllDirections lists every Direction value, in stable order.
func AllDirections() []string {
	return []string{
		string(DirectionCallers), string(DirectionCallees), string(DirectionBoth),
	}
}
