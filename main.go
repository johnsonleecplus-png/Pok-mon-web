// MARS 宝可梦卡牌库 · 单文件 Go server
// 数据: cards.db (SQLite, 20,444 卡 / 174 sets, 含 category 列)
// 模板: templates/
// 静态: pokemon.css + 自带 inline SVG
package main

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

// ============================================================================
// 数据模型
// ============================================================================

type Card struct {
	CardID      string
	SetID       string
	SetName     string
	SetSeries   string
	SetSymbol   string
	Number      string
	Name        string
	Supertype   string
	Subtypes    string
	HP          string
	Types       string
	Rarity      string
	Artist      string
	ImageSm     string
	ImageLg     string
	Release     string
	Year        int
	PTCGOMkt    string
	Cardmarkt   string
	Abilities   string
	Attacks     string
	Weakness    string
	Resistance  string
	RetreatCost string
	FlavorText  string
	NationalDex string
	RegMark     string
	EvolvesFrom string
	EvolvesTo   string
	Level       string
	Rules       string
	TcgplayerLow string
}

type SetRow struct {
	SetID     string
	SetName   string
	SetNameCN string
	Series    string
	Release   string
	Year      int
	Symbol    string
	CardCnt   int
}

type SidebarGroup struct {
	Series   string
	SeriesCN string
	Sets     []SetRow
}

type PageData struct {
	Title      string
	Sidebar    []SidebarGroup
	Active     string
	Total      int
	Filter     FilterState
	Cards      []Card
	Types      []string
	DetailCard *Card
}

type FilterState struct {
	Category string
	Rarity   string
	Sort     string
	Type     string
	Lang     string
	PriceMin float64
	PriceMax float64
	FavIDs   string
}

// 中文显示 -> 业务 category 码
var categoryLabelToCode = map[string]string{
	"全部":     "",
	"宝可梦":    "pokemon",
	"人物卡":    "supporter",
	"道具卡":    "item",
	"宝可梦道具":  "tool",
	"场地卡":    "stadium",
	"能量卡":    "energy",
}

// sidebar 大类中文翻译 (英文 series -> 中文)
var seriesCN = map[string]string{
	"Base":                  "基础",
	"Black & White":         "黑白",
	"Diamond & Pearl":       "钻石珍珠",
	"E-Card":                "电子卡",
	"EX":                    "EX 系列",
	"Gym":                   "道馆",
	"HeartGold & SoulSilver": "金银水晶",
	"Mega Evolution":        "超进化",
	"NP":                    "NP",
	"Neo":                   "新纪元",
	"Other":                 "其他",
	"POP":                   "POP 系列",
	"Platinum":              "白金",
	"Scarlet & Violet":      "朱紫",
	"Sun & Moon":            "日月",
	"Sword & Shield":        "剑盾",
	"XY":                    "XY",
}

var db *sql.DB
var tmpl *template.Template
var buildStamp int64 = time.Now().Unix()
var staticDir string

const (
	pageSize = 30
	types    = "草,火,水,电,超,斗,恶,钢,普,飞,地,龙,妖,Colorless"
)

var typeColorZH = map[string]string{
	"草": "#78C850", "火": "#F08030", "水": "#6890F0", "电": "#F8D030",
	"超": "#F85888", "斗": "#C03028", "恶": "#705848", "钢": "#B8B8D0",
	"普": "#A8A878", "飞": "#A890F0", "地": "#E0C068", "龙": "#7038F8", "妖": "#EE99AC",
}

// 中文类型名 -> 英文 (DB 存英文, 模板/UI 用中文, 查询时翻译)
var typeENMap = map[string]string{
	"草": "Grass", "火": "Fire", "水": "Water", "电": "Lightning",
	"超": "Psychic", "斗": "Fighting", "恶": "Darkness", "钢": "Metal",
	"普": "Colorless", "飞": "Flying", "地": "Fighting", "龙": "Dragon", "妖": "Fairy",
}

func main() {
	var err error
	db, err = sql.Open("sqlite", "cards.db?_journal=WAL&_cache_size=-10000&_synchronous=NORMAL")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// 先计算 staticDir, 后面的 syncParentCSS / calcBuildStamp 都依赖它
	wd, _ := os.Getwd()
	log.Printf("Working dir: %s", wd)
	staticDir = filepath.Join(wd, "static")
	log.Printf("Static dir: %s", staticDir)

	// 启动时自动同步父目录的 CSS 到子目录 (避免双副本丢失)
	syncParentCSS()
	buildStamp = calcBuildStamp()

	tmpl = template.Must(template.New("").Funcs(funcMap).ParseGlob("templates/*.html"))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleCover)
	mux.HandleFunc("GET /app", handleHome)
	mux.HandleFunc("GET /cards", handleCardsPartial)
	mux.HandleFunc("GET /cards/{id}", handleCardDetail)
	mux.HandleFunc("GET /api/sets", handleSetsJSON)
	mux.HandleFunc("GET /api/favorites", handleFavoritesList)
	mux.HandleFunc("POST /api/favorites", handleFavoriteAdd)
	mux.HandleFunc("DELETE /api/favorites/{card_id}", handleFavoriteRemove)
	mux.HandleFunc("GET /sidebar", handleSidebar)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	addr := ":8129"
	log.Printf("MARS-宝可梦卡牌库 → http://localhost%s", addr)
	if v := os.Getenv("DEBUG_NO_MIDDLEWARE"); v != "" {
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatal(err)
		}
	} else {
		handler := gzipMiddleware(cacheControlMiddleware(mux))
		if err := http.ListenAndServe(addr, handler); err != nil {
			log.Fatal(err)
		}
	}
}

// gzip 中间件: 对 HTML/CSS/JS 启用 gzip
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		defer gz.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		next.ServeHTTP(&gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	Writer io.Writer
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) { return g.Writer.Write(b) }

// cache-control 中间件: 静态资源长期缓存, HTML 短缓存
func cacheControlMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/static/"):
			w.Header().Set("Cache-Control", "public, max-age=2592000")
		case strings.HasPrefix(path, "/sidebar"), strings.HasPrefix(path, "/api/"):
			// favorites API 必须 no-cache (否则浏览器 5 分钟拿旧数据, 看不到刚 POST/DELETE 的结果)
			if strings.HasPrefix(path, "/api/favorites") {
				w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=300")
			}
		case path == "/cards" || path == "/app" || path == "/":
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		}
		next.ServeHTTP(w, r)
	})
}

// ============================================================================
// JSON 解析辅助
// ============================================================================

type Ability struct {
	Name string
	Type string
	Text string
}

type Attack struct {
	Name   string
	Type   string
	Cost   []string
	Damage string
	Text   string
}

type WeakRes struct {
	Type  string
	Value string
}

func parseJSONField[T any](s string) []T {
	if s == "" {
		return nil
	}
	var out []T
	trim := strings.TrimSpace(s)
	if strings.HasPrefix(trim, "[") {
		if err := json.Unmarshal([]byte(s), &out); err == nil {
			return out
		}
	}
	if strings.HasPrefix(trim, "{") {
		var one T
		if err := json.Unmarshal([]byte(s), &one); err == nil {
			return []T{one}
		}
	}
	return nil
}

var rarityMap = map[string][2]string{
	"Common":                     {"r-common", "C"},
	"Uncommon":                   {"r-uncommon", "U"},
	"Rare":                       {"r-rare", "R"},
	"Rare Holo":                  {"r-holo", "H"},
	"Rare Holo EX":               {"r-holo", "H"},
	"Rare Holo GX":               {"r-holo", "H"},
	"Rare Holo V":                {"r-holo", "H"},
	"Rare Holo VMAX":             {"r-holo", "H"},
	"Rare Holo VSTAR":            {"r-holo", "H"},
	"Rare Holo LV.X":             {"r-holo", "H"},
	"Trainer Gallery Rare Holo":  {"r-holo", "H"},
	"Rare Ultra":                 {"r-ultra", "UR"},
	"Ultra Rare":                 {"r-ultra", "UR"},
	"Rare Secret":                {"r-ultra", "UR"},
	"Rare Rainbow":               {"r-ultra", "UR"},
	"Rare ACE":                   {"r-ultra", "UR"},
	"ACE SPEC Rare":              {"r-ultra", "UR"},
	"Double Rare":                {"r-double", "RR"},
	"Illustration Rare":          {"r-illustration", "IR"},
	"Special Illustration Rare":  {"r-special", "SIR"},
	"Hyper Rare":                 {"r-hyper", "HR"},
	"Mega Hyper Rare":            {"r-hyper", "HR"},
	"Rare Shining":               {"r-special", "α"},
	"Rare Shiny":                 {"r-special", "α"},
	"Rare Holo Star":             {"r-special", "α"},
	"Shiny Rare":                 {"r-special", "α"},
	"Shiny Ultra Rare":           {"r-ultra", "UR"},
	"Rare BREAK":                 {"r-double", "B"},
	"Rare Prime":                 {"r-double", "P"},
	"Rare Prism Star":            {"r-special", "PS"},
	"LEGEND":                     {"r-double", "L"},
	"Amazing Rare":               {"r-illustration", "AR"},
	"Radiant Rare":               {"r-rare", "R"},
	"Promo":                      {"r-common", "P"},
	"Classic Collection":         {"r-common", "C"},
	"MEGA_ATTACK_RARE":           {"r-holo", "H"},
	"Black White Rare":           {"r-rare", "R"},
	"Rare Shiny GX":              {"r-holo", "H"},
}

// 稀有度中英对照 + 完整中文名
var rarityZh = map[string]string{
	"Common":                     "普通",
	"Uncommon":                   "非普通",
	"Rare":                       "稀有",
	"Rare Holo":                  "稀有闪",
	"Rare Holo EX":               "稀有EX",
	"Rare Holo GX":               "稀有GX",
	"Rare Holo V":                "稀有V",
	"Rare Holo VMAX":             "稀有VMAX",
	"Rare Holo VSTAR":            "稀有VSTAR",
	"Rare Holo LV.X":             "稀有LV.X",
	"Trainer Gallery Rare Holo":  "训练家画廊稀有闪",
	"Rare Ultra":                 "超稀有",
	"Ultra Rare":                 "超稀有",
	"Rare Secret":                "秘密稀有",
	"Rare Rainbow":               "彩虹稀有",
	"Rare ACE":                   "ACE稀有",
	"ACE SPEC Rare":              "ACE SPEC稀有",
	"Double Rare":                "双重稀有",
	"Illustration Rare":          "插画稀有",
	"Special Illustration Rare":  "特别插画稀有",
	"Hyper Rare":                 "金色稀有",
	"Mega Hyper Rare":            "Mega金色稀有",
	"Rare Shining":               "光辉稀有",
	"Rare Shiny":                 "闪光稀有",
	"Rare Holo Star":             "稀有闪星",
	"Shiny Rare":                 "闪光稀有",
	"Shiny Ultra Rare":           "闪光超稀有",
	"Rare Shiny GX":              "闪光GX",
	"Rare BREAK":                 "BREAK稀有",
	"Rare Prime":                 "Prime稀有",
	"Rare Prism Star":            "棱镜星稀有",
	"LEGEND":                     "传说",
	"Amazing Rare":               "惊奇稀有",
	"Radiant Rare":               "光辉稀有",
	"Promo":                      "特典",
	"Classic Collection":         "经典收藏",
	"MEGA_ATTACK_RARE":           "Mega攻击稀有",
	"Black White Rare":           "BW稀有",
}

var funcMap = template.FuncMap{
	"typeColor": func(zh string) string {
		if c, ok := typeColorZH[zh]; ok {
			return c
		}
		return "#A8A878"
	},
	"typeName": func(zh string) string {
		if n, ok := typeENMap[zh]; ok {
			return n
		}
		return zh
	},
	"formatPrice": func(s string) string {
		if s == "" {
			return "-"
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return s
		}
		return fmt.Sprintf("$%.2f", f)
	},
	"formatPriceShort": func(s string) string {
		if s == "" {
			return ""
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return s
		}
		if f < 1 {
			return fmt.Sprintf("%.0f¢", f*100)
		}
		return fmt.Sprintf("$%.0f", f)
	},
	"truncate": func(s string, n int) string {
		if len(s) > n {
			return s[:n] + "…"
		}
		return s
	},
	"split": func(s, sep string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(s, sep)
	},
	"abilities": func(s string) []Ability {
		return parseJSONField[Ability](s)
	},
	"attacks": func(s string) []Attack {
		return parseJSONField[Attack](s)
	},
	"weakres": func(s string) []WeakRes {
		return parseJSONField[WeakRes](s)
	},
	"rarityClass": func(rarity string) string {
		if v, ok := rarityMap[rarity]; ok {
			return v[0]
		}
		return "r-common"
	},
	"rarityAbbr": func(rarity string) string {
		if v, ok := rarityMap[rarity]; ok {
			return v[1]
		}
		return rarity
	},
	"rarityZh": func(rarity string) string {
		if z, ok := rarityZh[rarity]; ok {
			return z
		}
		return rarity
	},
	"dict": func(values ...any) map[string]any {
		m := make(map[string]any)
		for i := 0; i < len(values)-1; i += 2 {
			if key, ok := values[i].(string); ok {
				m[key] = values[i+1]
			}
		}
		return m
	},
	"hasChinese": func(name string) bool {
		for _, r := range name {
			if unicode.Is(unicode.Han, r) {
				return true
			}
		}
		return len(name) > 0
	},
	"addOne": func(i int) int { return i + 1 },
	"setCode": func(setID string) string {
		m := map[string]string{
			"base1": "BS", "base2": "JU", "base3": "FO", "base4": "BR",
			"base5": "TR", "gym1": "G1", "gym2": "G2", "neo1": "N1",
			"neo2": "N2", "neo3": "N3", "neo4": "N4",
		}
		if v, ok := m[setID]; ok {
			return v
		}
		s := setID
		for i, r := range s {
			if r >= 0x30 && r <= 0x39 {
				s = s[:i]
				break
			}
		}
		return strings.ToUpper(s)
	},
	// 静态资源版本戳: 用启动时刻 + 资源 mtime 算出, URL 后加 ?v=xxx 强制不缓存
	"isLocal": func(s string) bool {
		return strings.HasPrefix(s, "/static/")
	},
	"staticVer": func(p string) string {
		fp := filepath.Join(staticDir, strings.TrimPrefix(p, "/static/"))
		info, err := os.Stat(fp)
		if err != nil {
			return "?v=" + fmt.Sprint(buildStamp)
		}
		v := info.ModTime().Unix()
		if v < buildStamp {
			v = buildStamp
		}
		return fmt.Sprintf("?v=%d", v)
	},
	"assetVer": func() string {
		return fmt.Sprintf("?v=%d", buildStamp)
	},
}

// ============================================================================
// 路由 handler
// ============================================================================

func handleHome(w http.ResponseWriter, r *http.Request) {
	pg := PageData{
		Title:   "MARS-宝可梦卡牌库",
		Sidebar: loadSidebar(),
		Active:  "all",
		Filter:  FilterState{Category: "全部", Sort: "number", Lang: "zh"},
	}
	cards, total := queryCards(pg.Filter, "", 0, pageSize, "")
	pg.Total = total
	data := map[string]any{
		"Title":    pg.Title,
		"Sidebar":  pg.Sidebar,
		"Active":   pg.Active,
		"Filter":   pg.Filter,
		"Total":    pg.Total,
		"Cards":    cards,
		"PageSize": pageSize,
		"Types":    strings.Split(types, ","),
	}
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		log.Printf("render home: %v", err)
	}
}

func handleCardsPartial(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	priceMin, _ := strconv.ParseFloat(q.Get("price_min"), 64)
	priceMax, _ := strconv.ParseFloat(q.Get("price_max"), 64)
	f := FilterState{
		Category: defaultStr(q.Get("supertype"), "全部"),
		Type:     q.Get("type"),
		Rarity:   q.Get("rarity"),
		Sort:     defaultStr(q.Get("sort"), "number"),
		Lang:     defaultStr(q.Get("lang"), "zh"),
		PriceMin: priceMin,
		PriceMax: priceMax,
		FavIDs:   q.Get("fav_ids"),
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 0 {
		page = 0
	}
	setID := q.Get("set_id")
	qSearch := q.Get("q")
	cards, total := queryCards(f, setID, page, pageSize, qSearch)

	data := map[string]any{
		"Cards":    cards,
		"Total":    total,
		"Page":     page,
		"PageSize": pageSize,
		"HasMore":  (page+1)*pageSize < total,
		"Filter":   f,
		"SetID":    setID,
	}
	// Direct browser visit (no htmx header) — render full layout for bookmarking
	if r.Header.Get("HX-Request") == "" {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "cards_partial.html", data); err != nil {
			log.Printf("render cards: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}
		data["Sidebar"] = loadSidebar()
		data["Title"] = "MARS-宝可梦卡牌库"
		data["Active"] = setID
		data["Types"] = strings.Split(types, ",")
		data["CardsHTML"] = template.HTML(buf.String())
		if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
			log.Printf("render layout: %v", err)
		}
		return
	}

	if err := tmpl.ExecuteTemplate(w, "cards_partial.html", data); err != nil {
		log.Printf("render cards: %v", err)
		http.Error(w, err.Error(), 500)
	}
}

func handleCardDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	card, err := queryCardByID(id)
	if err != nil {
		http.Error(w, "card not found", 404)
		return
	}
	isHX := r.Header.Get("HX-Request") != ""
	if isHX {
		data := map[string]any{"Card": card}
		tmpl.ExecuteTemplate(w, "card_detail.html", data)
		return
	}
	pg := PageData{
		Title:   fmt.Sprintf("%s · MARS-宝可梦卡牌库", card.Name),
		Sidebar: loadSidebar(),
		Active:  "all",
		Filter:  FilterState{Category: "全部", Sort: "number", Lang: "zh"},
	}
	cards, total := queryCards(pg.Filter, "", 0, pageSize, "")
	pg.Total = total
	data := map[string]any{
		"Title":      pg.Title,
		"Sidebar":    pg.Sidebar,
		"Active":     pg.Active,
		"Filter":     pg.Filter,
		"Total":      pg.Total,
		"Cards":      cards,
		"PageSize":   pageSize,
		"Types":      strings.Split(types, ","),
		"DetailCard": card,
	}
	tmpl.ExecuteTemplate(w, "layout.html", data)
}

func handleCover(w http.ResponseWriter, r *http.Request) {
	if err := tmpl.ExecuteTemplate(w, "cover.html", nil); err != nil {
		log.Printf("render cover: %v", err)
	}
}

func handleSetsJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(loadSidebar())
}

// ===== 收藏: 与 DB 同步 =====

func handleFavoritesList(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT card_id FROM favorites ORDER BY created_at DESC")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ids)
}

func handleFavoriteAdd(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("card_id")
	if id == "" {
		http.Error(w, "card_id required", 400)
		return
	}
	_, err := db.Exec("INSERT OR IGNORE INTO favorites(card_id) VALUES(?)", id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "card_id": id})
}

func handleFavoriteRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("card_id")
	if id == "" {
		http.Error(w, "card_id required", 400)
		return
	}
	_, err := db.Exec("DELETE FROM favorites WHERE card_id=?", id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "card_id": id})
}

func handleSidebar(w http.ResponseWriter, r *http.Request) {
	sidebar := loadSidebar()
	tmpl.ExecuteTemplate(w, "sidebar.html", sidebar)
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ============================================================================
// DB 查询
// ============================================================================

func queryCards(f FilterState, setID string, page, size int, q string) ([]Card, int) {
	var (
		where []string
		args  []any
	)
	if setID != "" {
		where = append(where, "c.set_id = ?")
		args = append(args, setID)
	}
	// category 过滤: 中文标签 -> 业务码
	if code, ok := categoryLabelToCode[f.Category]; ok && code != "" {
		where = append(where, "c.category = ?")
		args = append(args, code)
	}
	if f.Type != "" {
		// 中文类型名 -> 英文类型名（DB 里存的是英文）
		typeEN := f.Type
		if en, ok := typeENMap[typeEN]; ok {
			typeEN = en
		}
		where = append(where, "c.types LIKE ?")
		args = append(args, "%"+typeEN+"%")
	}
	if f.Rarity != "" {
		where = append(where, "c.rarity = ?")
		args = append(args, f.Rarity)
	}
	if f.PriceMin > 0 {
		where = append(where, "CAST(NULLIF(c.tcgplayer_price_market, '') AS REAL) >= ?")
		args = append(args, f.PriceMin)
	}
	if f.PriceMax > 0 {
		where = append(where, "CAST(NULLIF(c.tcgplayer_price_market, '') AS REAL) <= ?")
		args = append(args, f.PriceMax)
	}
	if f.FavIDs != "" {
		ids := strings.Split(f.FavIDs, ",")
		placeholders := make([]string, len(ids))
		for i, id := range ids {
			placeholders[i] = "?"
			args = append(args, id)
		}
		where = append(where, "c.card_id IN ("+strings.Join(placeholders, ",")+")")
	}
	if q != "" {
		searchLike := "%" + q + "%"
		where = append(where, "(c.card_name LIKE ? OR c.card_number LIKE ? OR c.attacks LIKE ?)")
		args = append(args, searchLike, searchLike, searchLike)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	// total
	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM cards c "+whereSQL, args...).Scan(&total); err != nil {
		log.Printf("count: %v", err)
	}

	// order
	// 默认排序: set 内按 supertype 分组
	order := "ORDER BY c.set_release_date DESC, c.set_id, CASE c.supertype WHEN 'Pokémon' THEN 0 WHEN 'Trainer' THEN 1 ELSE 2 END, CAST(c.card_number AS INTEGER), c.card_number"
	switch f.Sort {
	case "price-desc":
		order = "ORDER BY CAST(NULLIF(c.tcgplayer_price_market, '') AS REAL) DESC"
	case "price-asc":
		order = "ORDER BY CAST(NULLIF(c.tcgplayer_price_market, '') AS REAL) ASC"
	case "name":
		order = "ORDER BY c.card_name"
	case "rarity":
		order = "ORDER BY c.rarity, c.card_name"
	}

	limit := size
	offset := page * size

	// 「全部」模式且无其他筛选时, 跨 category 轮询混合, 避免单类占满
	interleaved := f.Category == "全部" && f.Type == "" && f.Rarity == "" &&
		f.PriceMin == 0 && f.PriceMax == 0 && f.FavIDs == "" && setID == "" &&
		f.Sort == "number" && q == ""

	if interleaved {
		// 按 category 拆 6 桶, 每桶各取 limit, 然后轮询合并
		groups := []string{"pokemon", "supporter", "item", "tool", "stadium", "energy"}
		perGroup := limit
		merged := make([]Card, 0, limit)
		cursors := make([]int, len(groups))
		// 每个 category 桶轮询穿插
		for len(merged) < limit {
			added := 0
			for gi, st := range groups {
				if len(merged) >= limit {
					break
				}
				start := cursors[gi]
				end := start + 1
				if end > perGroup {
					end = perGroup
				}
				groupArgs := append([]any{}, args...)
				groupArgs = append(groupArgs, st, end-start, start)
				// 处理 whereSQL 为空的情况: 加 WHERE 关键字
				stWhere := whereSQL
				if stWhere == "" {
					stWhere = "WHERE c.category = ?"
				} else {
					stWhere = whereSQL + " AND c.category = ?"
				}
				q := fmt.Sprintf(`
					SELECT c.card_id, c.set_id, c.set_name, c.set_series, s.set_symbol_url,
					       c.card_number, c.card_name, c.supertype, c.subtypes, c.hp, c.types, c.rarity,
					       c.artist, c.image_small, c.image_large, c.set_release_date, c.year,
					       c.tcgplayer_price_market, c.cardmarket_price_avg
					FROM cards c
					LEFT JOIN sets s ON s.set_id = c.set_id
					%s %s LIMIT ? OFFSET ?
				`, stWhere, order)
				rows, err := db.Query(q, groupArgs...)
				if err != nil {
					log.Printf("interleaved query group %s: %v", st, err)
					continue
				}
				count := 0
				for rows.Next() {
					var c Card
					if err := rows.Scan(&c.CardID, &c.SetID, &c.SetName, &c.SetSeries, &c.SetSymbol,
						&c.Number, &c.Name, &c.Supertype, &c.Subtypes, &c.HP, &c.Types, &c.Rarity,
						&c.Artist, &c.ImageSm, &c.ImageLg, &c.Release, &c.Year, &c.PTCGOMkt, &c.Cardmarkt); err != nil {
						continue
					}
					if len(merged) < limit {
						merged = append(merged, c)
						added++
					}
					count++
				}
				rows.Close()
				cursors[gi] += count
			}
			// 6 桶都没新数据了, 跳出
			if added == 0 {
				break
			}
		}
		return merged, total
	}

	rows, err := db.Query(fmt.Sprintf(`
		SELECT c.card_id, c.set_id, c.set_name, c.set_series, s.set_symbol_url,
		       c.card_number, c.card_name, c.supertype, c.subtypes, c.hp, c.types, c.rarity,
		       c.artist, c.image_small, c.image_large, c.set_release_date, c.year,
		       c.tcgplayer_price_market, c.cardmarket_price_avg
		FROM cards c
		LEFT JOIN sets s ON s.set_id = c.set_id
		%s %s LIMIT ? OFFSET ?
	`, whereSQL, order), append(args, limit, offset)...)
	if err != nil {
		log.Printf("query: %v", err)
		return nil, 0
	}
	defer rows.Close()
	out := make([]Card, 0, limit)
	for rows.Next() {
		var c Card
		if err := rows.Scan(&c.CardID, &c.SetID, &c.SetName, &c.SetSeries, &c.SetSymbol,
			&c.Number, &c.Name, &c.Supertype, &c.Subtypes, &c.HP, &c.Types, &c.Rarity,
			&c.Artist, &c.ImageSm, &c.ImageLg, &c.Release, &c.Year, &c.PTCGOMkt, &c.Cardmarkt); err != nil {
			log.Printf("scan: %v", err)
			continue
		}
		out = append(out, c)
	}
	return out, total
}

func queryCardByID(id string) (*Card, error) {
	row := db.QueryRow(`
		SELECT c.card_id, c.set_id, c.set_name, c.set_series, s.set_symbol_url,
		       c.card_number, c.card_name, c.supertype, c.subtypes, c.hp, c.types, c.rarity,
		       c.artist, c.image_small, c.image_large, c.set_release_date, c.year,
		       c.tcgplayer_price_market, c.tcgplayer_price_low, c.cardmarket_price_avg,
		       c.abilities, c.attacks, c.weakness, c.resistance, c.retreat_cost,
		       c.flavor_text, c.national_pokedex, c.regulation_mark,
		       c.evolves_from, c.evolves_to, c.level, c.rules
		FROM cards c
		LEFT JOIN sets s ON s.set_id = c.set_id
		WHERE c.card_id = ?
	`, id)
	var c Card
	err := row.Scan(&c.CardID, &c.SetID, &c.SetName, &c.SetSeries, &c.SetSymbol,
		&c.Number, &c.Name, &c.Supertype, &c.Subtypes, &c.HP, &c.Types, &c.Rarity,
		&c.Artist, &c.ImageSm, &c.ImageLg, &c.Release, &c.Year, &c.PTCGOMkt, &c.TcgplayerLow, &c.Cardmarkt,
		&c.Abilities, &c.Attacks, &c.Weakness, &c.Resistance, &c.RetreatCost,
		&c.FlavorText, &c.NationalDex, &c.RegMark,
		&c.EvolvesFrom, &c.EvolvesTo, &c.Level, &c.Rules)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func loadSidebar() []SidebarGroup {
	rows, err := db.Query(`
		SELECT s.set_series, s.set_id, s.set_name, COALESCE(s.set_name_cn, s.set_name),
		       s.set_release_date, s.year, s.set_symbol_url, s.card_count
		FROM sets s
		ORDER BY s.set_release_date DESC, s.set_id
	`)
	if err != nil {
		log.Printf("load sidebar: %v", err)
		return nil
	}
	defer rows.Close()
	bySeries := map[string][]SetRow{}
	order := []string{}
	for rows.Next() {
		var s SetRow
		var cnt sql.NullInt64
		if err := rows.Scan(&s.Series, &s.SetID, &s.SetName, &s.SetNameCN, &s.Release, &s.Year, &s.Symbol, &cnt); err != nil {
			continue
		}
		if cnt.Valid {
			s.CardCnt = int(cnt.Int64)
		}
		if _, ok := bySeries[s.Series]; !ok {
			order = append(order, s.Series)
		}
		bySeries[s.Series] = append(bySeries[s.Series], s)
	}
	out := make([]SidebarGroup, 0, len(order))
	for _, series := range order {
		grp := SidebarGroup{Series: series, Sets: bySeries[series]}
		if cn, ok := seriesCN[series]; ok {
			grp.SeriesCN = cn
		} else {
			grp.SeriesCN = series
		}
		out = append(out, grp)
	}
	return out
}

// calcBuildStamp: 启动时取所有关键静态资源的最大 mtime, 任何文件改动 -> buildStamp 变化 -> URL ?v= 变 -> 浏览器强刷
func calcBuildStamp() int64 {
	if staticDir == "" {
		return time.Now().Unix()
	}
	latest := time.Now().Unix()
	walk := func(rel string) {
		fp := filepath.Join(staticDir, rel)
		info, err := os.Stat(fp)
		if err != nil {
			return
		}
		if t := info.ModTime().Unix(); t > latest {
			latest = t
		}
	}
	// 关键文件
	walk("pokemon.css")
	walk("pokeball.svg")
	walk("pokes")
	// templates
	if matches, err := filepath.Glob("templates/*.html"); err == nil {
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil {
				if t := info.ModTime().Unix(); t > latest {
					latest = t
				}
			}
		}
	}
	return latest
}

// syncParentCSS: 启动时把父目录的 pokemon.css 同步到子目录, 避免双副本丢失
func syncParentCSS() {
	wd, _ := os.Getwd()
	parent := filepath.Dir(wd)
	src := filepath.Join(parent, "pokemon.css")
	dst := filepath.Join(wd, "static", "pokemon.css")
	srcStat, err := os.Stat(src)
	if err != nil {
		return
	}
	dstStat, err := os.Stat(dst)
	if err == nil && srcStat.Size() == dstStat.Size() && srcStat.ModTime().Equal(dstStat.ModTime()) {
		return
	}
	if data, err := os.ReadFile(src); err == nil {
		_ = os.WriteFile(dst, data, 0644)
		log.Printf("CSS 同步: %s -> %s (%dB)", src, dst, len(data))
	}
}
