package stock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/bytetopia/quote0-link/app/internal/plugin"
)

func TestCode(t *testing.T) {
	cases := map[string]string{
		"600519": "sh600519", "000001": "sz000001", "300750": "sz300750", "830799": "bj830799",
		"sh000001": "sh000001", "SZ399001": "sz399001", "700": "hk00700", "00700": "hk00700", "HK9988": "hk09988",
		"aapl": "usAAPL", "us.tsla": "usTSLA", "usAAPL": "usAAPL", "USB": "usUSB", "brk.b": "usBRK.B",
	}
	for input, want := range cases {
		if got, err := Code(input); err != nil || got != want {
			t.Errorf("Code(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "1234567", "中文", "a b"} {
		if got, err := Code(input); err == nil {
			t.Errorf("Code(%q) = %q, want error", input, got)
		}
	}
}

func TestNormalize(t *testing.T) {
	source := &Source{}
	settings, err := source.Normalize(map[string]string{"symbols": " 600519，00700 aapl 600519 "})
	if err != nil || settings["symbols"] != "sh600519,hk00700,usAAPL" {
		t.Fatalf("got %v %v", settings, err)
	}
	if _, err := source.Normalize(map[string]string{"symbols": "1 2 3 4"}); err == nil {
		t.Fatal("accepted four symbols")
	}
	if _, err := source.Normalize(nil); err == nil {
		t.Fatal("accepted no symbols")
	}
}

func fields(values map[int]string) string {
	parts := make([]string, 50)
	for index, value := range values {
		parts[index] = value
	}
	return strings.Join(parts, "~")
}

func TestParseQuotes(t *testing.T) {
	body := `v_sh600519="` + fields(map[int]string{1: "贵州茅台", 2: "600519", 3: "1450.00", 30: "20261123135902", 31: "-12.50", 32: "-0.85"}) + `";` + "\n" +
		`v_pv_none_match="1";`
	quotes, err := parseQuotes(body, []string{"sh600519"})
	if err != nil || quotes[0].name != "贵州茅台" || quotes[0].time != "11-23 13:59" || quotes[0].percent != "-0.85" {
		t.Fatalf("got %+v %v", quotes, err)
	}
	if _, err := parseQuotes(body, []string{"sh600519", "hk00700"}); err == nil {
		t.Fatal("missing symbol not reported")
	}
}

func TestMarketTime(t *testing.T) {
	for input, want := range map[string]string{
		"20261123135902": "11-23 13:59", "2026-11-20 16:00:04": "11-20 16:00", "2026/11/23 13:59:42": "11-23 13:59", "x": "x",
	} {
		if got := marketTime(input); got != want {
			t.Errorf("marketTime(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFetch(t *testing.T) {
	gbk, _ := simplifiedchinese.GBK.NewEncoder().String(
		`v_sh600519="` + fields(map[int]string{1: "贵州茅台", 2: "600519", 3: "1450.00", 30: "20261123135902", 31: "12.50", 32: "0.85"}) + `";` + "\n" +
			`v_hk00700="` + fields(map[int]string{1: "腾讯控股", 2: "00700", 3: "520.000", 30: "2026/11/23 13:59:42", 31: "-1.0", 32: "-0.19"}) + `";`)
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query = request.URL.RawQuery
		_, _ = writer.Write([]byte(gbk))
	}))
	defer server.Close()
	source := &Source{URL: server.URL + "/?q=", Client: server.Client()}

	card, err := source.Fetch(context.Background(), plugin.Job{Name: "自选", Settings: map[string]string{"symbols": "sh600519"}})
	if err != nil || query != "q=sh600519" || card.Title != "贵州茅台" || card.Body != "1450.00\n+12.50  +0.85%" {
		t.Fatalf("single: %+v %v (query %q)", card, err, query)
	}
	card, err = source.Fetch(context.Background(), plugin.Job{Name: "自选", Settings: map[string]string{"symbols": "sh600519,hk00700"}})
	if err != nil || card.Title != "自选" || card.Body != "贵州茅台  1450.00  +0.85%\n腾讯控股  520.000  -0.19%" {
		t.Fatalf("multi: %+v %v", card, err)
	}
}
