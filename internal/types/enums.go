package types

// Kind is the closed set of chunk/symbol kinds the indexer emits. The
// underlying type is string so the existing JSON/SQL serialization
// keeps working unchanged; existing struct fields can stay typed as
// `string` while new APIs that dispatch on kind take `Kind` for
// readability and refactor safety.
type Kind string

const (
	KindFunction  Kind = "function"
	KindMethod    Kind = "method"
	KindClass     Kind = "class"
	KindType      Kind = "type"
	KindInterface Kind = "interface"
	KindHeader    Kind = "header"
	KindBlock     Kind = "block"
	KindVariable  Kind = "variable"
	KindConstant  Kind = "constant"
)

// Valid reports whether k is a known Kind. Useful at trust boundaries
// (CLI flags, MCP tool args) where caller-supplied strings need
// validation.
func (k Kind) Valid() bool {
	switch k {
	case KindFunction, KindMethod, KindClass, KindType, KindInterface,
		KindHeader, KindBlock, KindVariable, KindConstant:
		return true
	}
	return false
}

// Visibility is the closed set of symbol visibility strings.
type Visibility string

const (
	VisibilityPublic   Visibility = "public"
	VisibilityPrivate  Visibility = "private"
	VisibilityInternal Visibility = "internal"
	VisibilityExported Visibility = "exported"
)

// Valid reports whether v is a known Visibility.
func (v Visibility) Valid() bool {
	switch v {
	case VisibilityPublic, VisibilityPrivate, VisibilityInternal, VisibilityExported:
		return true
	}
	return false
}

// EdgeKind is the closed set of edge kinds in the call/import graph.
type EdgeKind string

const (
	EdgeKindCalls      EdgeKind = "calls"
	EdgeKindImports    EdgeKind = "imports"
	EdgeKindTypeRef    EdgeKind = "type_ref"
	EdgeKindInherits   EdgeKind = "inherits"
	EdgeKindImplements EdgeKind = "implements"
)

// Valid reports whether k is a known EdgeKind.
func (k EdgeKind) Valid() bool {
	switch k {
	case EdgeKindCalls, EdgeKindImports, EdgeKindTypeRef, EdgeKindInherits, EdgeKindImplements:
		return true
	}
	return false
}

// EmbeddingStatus is the closed set of file-level embedding states.
type EmbeddingStatus string

const (
	EmbeddingStatusPending  EmbeddingStatus = "pending"
	EmbeddingStatusPartial  EmbeddingStatus = "partial"
	EmbeddingStatusComplete EmbeddingStatus = "complete"
)

// Valid reports whether s is a known EmbeddingStatus.
func (s EmbeddingStatus) Valid() bool {
	switch s {
	case EmbeddingStatusPending, EmbeddingStatusPartial, EmbeddingStatusComplete:
		return true
	}
	return false
}

// ParseQuality is the closed set of file-level parse outcomes.
type ParseQuality string

const (
	ParseQualityFull        ParseQuality = "full"
	ParseQualityPartial     ParseQuality = "partial"
	ParseQualityError       ParseQuality = "error"
	ParseQualityUnparseable ParseQuality = "unparseable"
)

// Valid reports whether q is a known ParseQuality.
func (q ParseQuality) Valid() bool {
	switch q {
	case ParseQualityFull, ParseQualityPartial, ParseQualityError, ParseQualityUnparseable:
		return true
	}
	return false
}
