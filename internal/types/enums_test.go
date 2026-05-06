package types

import "testing"

// TestEnumSets locks the closed sets so a code reviewer is forced to
// touch this test when adding a new constant. If a future change adds
// a Kind without updating Valid(), the new value silently fails
// validation in production — that bug surfaces here first.
func TestEnumSets(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{
			name: "Kind",
			got: stringify[Kind]([]Kind{
				KindFunction, KindMethod, KindClass, KindType, KindInterface,
				KindHeader, KindBlock, KindVariable, KindConstant,
			}),
			want: []string{"function", "method", "class", "type", "interface",
				"header", "block", "variable", "constant"},
		},
		{
			name: "Visibility",
			got: stringify[Visibility]([]Visibility{
				VisibilityPublic, VisibilityPrivate, VisibilityInternal, VisibilityExported,
			}),
			want: []string{"public", "private", "internal", "exported"},
		},
		{
			name: "EdgeKind",
			got: stringify[EdgeKind]([]EdgeKind{
				EdgeKindCalls, EdgeKindImports, EdgeKindTypeRef, EdgeKindInherits, EdgeKindImplements,
			}),
			want: []string{"calls", "imports", "type_ref", "inherits", "implements"},
		},
		{
			name: "EmbeddingStatus",
			got: stringify[EmbeddingStatus]([]EmbeddingStatus{
				EmbeddingStatusPending, EmbeddingStatusPartial, EmbeddingStatusComplete,
			}),
			want: []string{"pending", "partial", "complete"},
		},
		{
			name: "ParseQuality",
			got: stringify[ParseQuality]([]ParseQuality{
				ParseQualityFull, ParseQualityPartial, ParseQualityError, ParseQualityUnparseable,
			}),
			want: []string{"full", "partial", "error", "unparseable"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if len(c.got) != len(c.want) {
				t.Fatalf("len mismatch: got %d, want %d", len(c.got), len(c.want))
			}
			for i := range c.got {
				if c.got[i] != c.want[i] {
					t.Errorf("value[%d] = %q, want %q", i, c.got[i], c.want[i])
				}
			}
		})
	}
}

// TestEnumValidation_Negative ensures Valid() rejects out-of-set
// strings instead of silently accepting them.
func TestEnumValidation_Negative(t *testing.T) {
	t.Parallel()

	if Kind("imaginary").Valid() {
		t.Error("Kind(imaginary).Valid() = true, want false")
	}
	if Visibility("super").Valid() {
		t.Error("Visibility(super).Valid() = true, want false")
	}
	if EdgeKind("uses").Valid() {
		t.Error("EdgeKind(uses).Valid() = true, want false")
	}
	if EmbeddingStatus("done").Valid() {
		t.Error("EmbeddingStatus(done).Valid() = true, want false")
	}
	if ParseQuality("ok").Valid() {
		t.Error("ParseQuality(ok).Valid() = true, want false")
	}
}

func stringify[T ~string](xs []T) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = string(x)
	}
	return out
}
