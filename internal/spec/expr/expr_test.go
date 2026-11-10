package expr

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		src     string
		scope   Scope
		want    Result
		wantErr string
	}{
		{src: "data.text == ''", want: Bool},
		{src: "steps.review.output.decision == 'rejected' && attempt < 3", want: Bool},
		{src: "data.items", want: List},
		{src: "[1, 2, 3]", want: List},
		{src: "data.text + '!'", want: Any},
		{src: "item.size > index", scope: ScopeItem, want: Bool},
		{src: "has(data.review) ? data.review.reason : ''", want: Any},
		{src: "1 + 1", want: Bool, wantErr: "must evaluate to bool, got int"},
		{src: "'x'", want: List, wantErr: "must evaluate to list, got string"},
		{src: "item", want: Any, wantErr: "undeclared reference to 'item'"},
		{src: "data.(", want: Any, wantErr: "Syntax error"},
		{src: "attempt + 'x'", want: Any, wantErr: "found no matching overload"},
	} {
		err := Check(tc.src, tc.scope, tc.want)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%q: unexpected error %v", tc.src, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%q: error %v, want %q", tc.src, err, tc.wantErr)
		case err != nil && strings.Contains(err.Error(), "\n"):
			t.Errorf("%q: error must be one line: %q", tc.src, err)
		}
	}
}
