// Package stock is an active integration that shows quotes from Tencent's
// public quote endpoint (A shares, Hong Kong and US stocks).
package stock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/bytetopia/quote0-link/app/internal/plugin"
	"github.com/bytetopia/quote0-link/app/internal/render"
)

const maxSymbols = 3

var (
	marketCode = regexp.MustCompile(`^(sh|sz|bj)\d{6}$|^hk\d{5}$`)
	aShare     = regexp.MustCompile(`^\d{6}$`)
	hkShare    = regexp.MustCompile(`^\d{1,5}$`)
	usTicker   = regexp.MustCompile(`^[A-Z][A-Z0-9.\-]{0,9}$`)
	quoteLine  = regexp.MustCompile(`v_([A-Za-z0-9.\-]+)="([^"]*)"`)
	separators = regexp.MustCompile(`[\s,，;；、]+`)
)

func init() { plugin.RegisterSource(&Source{URL: "https://qt.gtimg.cn/q=", Client: &http.Client{}}) }

type Source struct {
	URL    string
	Client *http.Client
}

func (*Source) Info() plugin.Info {
	return plugin.Info{ID: "stock", Name: "股票行情", Note: "腾讯行情接口，支持 A 股（600519）、港股（00700）和美股（AAPL），每个任务最多 3 个代码。"}
}

func (*Source) Fields() []plugin.Field {
	return []plugin.Field{{
		Key: "symbols", Label: "股票代码", Placeholder: "600519, 00700, AAPL", Required: true,
		Help: "最多 3 个，用逗号或空格分隔；一个代码显示大字价格，多个代码显示列表。",
	}}
}

// DefaultSchedule suggests trading hours for new tasks.
func (*Source) DefaultSchedule() plugin.Schedule {
	return plugin.Schedule{Days: []int{1, 2, 3, 4, 5}, Start: "09:30", End: "16:00", Interval: 5}
}

func (*Source) Normalize(settings map[string]string) (map[string]string, error) {
	var codes []string
	for _, symbol := range separators.Split(strings.TrimSpace(settings["symbols"]), -1) {
		if symbol == "" {
			continue
		}
		code, err := Code(symbol)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(codes, code) {
			codes = append(codes, code)
		}
	}
	if len(codes) == 0 || len(codes) > maxSymbols {
		return nil, fmt.Errorf("请填写 1 到 %d 个股票代码", maxSymbols)
	}
	return map[string]string{"symbols": strings.Join(codes, ",")}, nil
}

// Code turns user input such as "600519", "00700", "aapl", "sh000001" or
// "us.TSLA" into Tencent's market-prefixed code.
func Code(input string) (string, error) {
	raw := strings.TrimSpace(input)
	value := strings.ToUpper(raw)
	if lower := strings.ToLower(raw); marketCode.MatchString(lower) {
		return lower, nil
	}
	if len(value) > 2 && strings.HasPrefix(value, "HK") && hkShare.MatchString(value[2:]) {
		value = value[2:]
	}
	// The US market must be explicit ("us.AAPL" or the stored form "usAAPL"),
	// otherwise tickers such as USB would be misread.
	if len(value) > 3 && (value[:3] == "US." || value[:3] == "US:") {
		if usTicker.MatchString(value[3:]) {
			return "us" + value[3:], nil
		}
	} else if strings.HasPrefix(raw, "us") && usTicker.MatchString(raw[2:]) {
		return raw, nil
	}
	switch {
	case aShare.MatchString(value):
		switch value[0] {
		case '6', '5', '9':
			return "sh" + value, nil
		case '4', '8':
			return "bj" + value, nil
		default:
			return "sz" + value, nil
		}
	case hkShare.MatchString(value):
		return "hk" + strings.Repeat("0", 5-len(value)) + value, nil
	case usTicker.MatchString(value):
		return "us" + value, nil
	}
	return "", fmt.Errorf("无法识别的股票代码：%q", raw)
}

func (source *Source) Fetch(ctx context.Context, job plugin.Job) (render.Card, error) {
	symbols := strings.Split(job.Settings["symbols"], ",")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL+strings.Join(symbols, ","), nil)
	if err != nil {
		return render.Card{}, err
	}
	request.Header.Set("User-Agent", "Quote0-Link")
	response, err := source.Client.Do(request)
	if err != nil {
		return render.Card{}, fmt.Errorf("行情请求失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return render.Card{}, fmt.Errorf("行情接口返回 %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(simplifiedchinese.GBK.NewDecoder().Reader(response.Body), 256<<10))
	if err != nil {
		return render.Card{}, err
	}
	quotes, err := parseQuotes(string(body), symbols)
	if err != nil {
		return render.Card{}, err
	}
	return card(job.Name, quotes), nil
}

type quote struct {
	code, name, price, change, percent, time string
}

func parseQuotes(body string, symbols []string) ([]quote, error) {
	found := map[string]quote{}
	for _, match := range quoteLine.FindAllStringSubmatch(body, -1) {
		fields := strings.Split(match[2], "~")
		if len(fields) < 33 || fields[3] == "" {
			continue
		}
		found[strings.ToLower(match[1])] = quote{
			code: fields[2], name: fields[1], price: fields[3],
			change: fields[31], percent: fields[32], time: marketTime(fields[30]),
		}
	}
	quotes := make([]quote, 0, len(symbols))
	for _, symbol := range symbols {
		item, ok := found[strings.ToLower(symbol)]
		if !ok {
			return nil, fmt.Errorf("未找到 %s 的行情", symbol)
		}
		quotes = append(quotes, item)
	}
	if len(quotes) == 0 {
		return nil, errors.New("没有行情数据")
	}
	return quotes, nil
}

// marketTime shortens "20261123135902", "2026-11-20 16:00:04" or
// "2026/11/23 13:59:42" to "11-23 13:59". It is the exchange's quote time, so
// an unchanged market produces an identical card and no e-ink refresh.
func marketTime(value string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, value)
	if len(digits) < 12 {
		return value
	}
	return digits[4:6] + "-" + digits[6:8] + " " + digits[8:10] + ":" + digits[10:12]
}

func signed(value string) string {
	if number, err := strconv.ParseFloat(value, 64); err == nil && number > 0 && !strings.HasPrefix(value, "+") {
		return "+" + value
	}
	return value
}

func shorten(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit-1]) + "…"
}

func card(name string, quotes []quote) render.Card {
	if len(quotes) == 1 {
		item := quotes[0]
		return render.Card{
			Template: "card", Title: shorten(item.name, 12), Subtitle: strings.TrimSpace(item.code + " · " + item.time),
			Body: item.price + "\n" + signed(item.change) + "  " + signed(item.percent) + "%",
		}
	}
	lines := make([]string, 0, len(quotes))
	for _, item := range quotes {
		lines = append(lines, shorten(item.name, 5)+"  "+item.price+"  "+signed(item.percent)+"%")
	}
	return render.Card{Template: "card", Title: name, Subtitle: "行情 · " + quotes[0].time, Body: strings.Join(lines, "\n")}
}
