package codegen

import (
	"testing"

	"goa.design/goa/codegen"
	"goa.design/goa/http/codegen/testdata"
	httpdesign "goa.design/goa/http/design"
)

func TestHandlerInit(t *testing.T) {
	const genpkg = "gen"
	cases := []struct {
		Name string
		DSL  func()
		Code string
	}{
		{"no payload no result", testdata.ServerNoPayloadNoResultDSL, testdata.ServerNoPayloadNoResultHandlerConstructorCode},
		{"payload no result", testdata.ServerPayloadNoResultDSL, testdata.ServerPayloadNoResultHandlerConstructorCode},
		{"no payload result", testdata.ServerNoPayloadResultDSL, testdata.ServerNoPayloadResultHandlerConstructorCode},
		{"payload result", testdata.ServerPayloadResultDSL, testdata.ServerPayloadResultHandlerConstructorCode},
		{"payload result error", testdata.ServerPayloadResultErrorDSL, testdata.ServerPayloadResultErrorHandlerConstructorCode},
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			RunHTTPDSL(t, c.DSL)
			fs := ServerFiles(genpkg, httpdesign.Root)
			if len(fs) != 2 {
				t.Fatalf("got %d files, expected two", len(fs))
			}
			sections := fs[0].SectionTemplates
			if len(sections) < 6 {
				t.Fatalf("got %d sections, expected a least 6", len(sections))
			}
			code := codegen.SectionCode(t, sections[5])
			if code != c.Code {
				t.Errorf("invalid code, got:\n%s\ngot vs. expected:\n%s", code, codegen.Diff(t, code, c.Code))
			}
		})
	}
}
