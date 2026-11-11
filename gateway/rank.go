package main

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Card matching and ranking. A release must name the card (title, original title or the typed
// query) and, for a film, fall within a year of it; releases below the seeder floor are dropped. The rest are
// scored, sorted best first and capped. Lampa re-sorts results itself, by the field named in its
// "torrents_sort" setting (lampa-source torrents.js:288-321); the plugin sets that to "Rank".

// card is what Lampa tells us about the title being searched (lampa-source core/api/sources/parser.js:291-311).
type card struct {
	names  [][]string // token sequences, any of which must appear in a release title
	year   int
	serial bool
	prefs  prefs
}

// prefs are the viewer's ranking preferences and device class. The plugin keeps them in
// Lampa's settings on each device and puts them into the search URL's path, because Lampa builds the query
// itself: …/jackett/p/l=uk,r=2160,h=1,d=android/api/v2.0/indexers/… The zero value is the household default.
type prefs struct {
	lang   string // first audio language: "ru" (default), "uk" or "en"
	res    int    // preferred resolution: 1080 (default), 2160 or 720
	noHDR  bool   // an SDR screen: HDR and Dolby Vision releases sink
	device string // Lampa.Platform: android, tizen, webos, browser, …; looked up in the vocabulary's devices
}

var deviceName = regexp.MustCompile(`^[a-z_]{1,16}$`)

// prefsFrom reads "l=uk,r=2160,h=0,d=android". Unknown keys and values are ignored, so an old or tampered
// URL falls back to the defaults instead of failing the search.
func prefsFrom(seg string) prefs {
	var p prefs
	for _, kv := range strings.Split(seg, ",") {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case k == "l" && (v == "ru" || v == "uk" || v == "en"):
			p.lang = v
		case k == "r" && (v == "2160" || v == "1080" || v == "720"):
			p.res, _ = strconv.Atoi(v)
		case k == "h":
			p.noHDR = v == "0"
		case k == "d" && deviceName.MatchString(v):
			p.device = v
		}
	}
	return p
}

var yearSuffix = regexp.MustCompile(`\s*\b(?:19|20)[0-9]{2}\s*$`)

func cardFrom(q map[string][]string) card {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 && v[0] != "undefined" && v[0] != "null" {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	c := card{serial: get("is_serial") == "2"}
	c.year, _ = strconv.Atoi(get("year"))
	for _, n := range []string{get("title"), get("title_original"), yearSuffix.ReplaceAllString(get("Query"), "")} {
		if t := tokens(n); len(t) > 0 {
			c.names = append(c.names, t)
		}
	}
	return c
}

// foldMarks maps lower-case Latin letters with diacritics, and ё, to their base letters: card titles carry
// them ("Shōgun", "Amélie") where release titles often do not.
var foldMarks = strings.NewReplacer(
	"ā", "a", "á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a",
	"ē", "e", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"ī", "i", "í", "i", "ì", "i", "î", "i", "ï", "i",
	"ō", "o", "ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o",
	"ū", "u", "ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c", "ý", "y", "ÿ", "y", "ё", "е",
)

// tokens lowercases, folds diacritics and splits on anything that is not a letter or digit.
func tokens(s string) []string {
	s = foldMarks.Replace(strings.ToLower(s))
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func containsSeq(hay, needle []string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		ok := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// matches reports whether a release belongs to the card: named checks the title, inYears the parsed year.
func (c card) matches(title string, p release) bool { return c.named(title) && c.inYears(p) }

// named reports whether the title names the card. No names means no card (a free-text search). It needs no
// parse, so the search filters on it before the regex work.
func (c card) named(title string) bool {
	if len(c.names) == 0 {
		return true
	}
	t := tokens(title)
	for _, n := range c.names {
		if containsSeq(t, n) {
			return true
		}
	}
	return false
}

func (c card) inYears(p release) bool {
	if c.year > 0 && p.Year > 0 {
		if c.serial {
			return p.Year >= c.year-1 // later seasons carry later years
		}
		return p.Year >= c.year-1 && p.Year <= c.year+1
	}
	return true
}

// Default preferences until profiles carry their own: the household's languages in the
// default order, 1080p first (2160p remuxes are heavy for zero-upload streaming on a Pi), no AV1 (many TVs
// cannot decode it), never 3D or camera copies.
var (
	// By preferred resolution; 1080 is the default (2160 remuxes are heavy for zero-upload streaming on a Pi).
	// An explicit 2160 or 720 choice outweighs the seeder bonus (4·log2, up to about 45), so those releases
	// lead unless they are barely seeded; the default keeps the measured mix of resolution and seeders.
	resolutionScore = map[int]map[int]float64{
		1080: {1080: 30, 2160: 25, 720: 20, 480: 5, 0: 10},
		2160: {2160: 55, 1080: 25, 720: 15, 480: 5, 0: 10},
		720:  {720: 55, 1080: 20, 480: 10, 2160: 5, 0: 10},
	}
	sourceScore = map[string]float64{"bluray": 15, "web-dl": 15, "remux": 10, "webrip": 10, "hdtv": 5, "dvd": 2, "": 5, "cam": -50}
	// By first audio language; the household's other languages follow.
	languageScore = map[string]map[string]float64{
		"ru": {"ru": 20, "uk": 15, "pl": 10, "en": 5},
		"uk": {"uk": 20, "ru": 15, "pl": 10, "en": 5},
		"en": {"en": 20, "ru": 15, "uk": 10, "pl": 5},
	}
	voiceScore = map[string]float64{"dub": 10, "mvo": 7, "dvo": 4, "avo": 2}
)

func score(p release, seeders int, pr prefs) float64 {
	res, lang := pr.res, pr.lang
	if resolutionScore[res] == nil {
		res = 1080
	}
	if languageScore[lang] == nil {
		lang = "ru"
	}
	s := resolutionScore[res][p.Resolution] + sourceScore[p.Source]
	best := 0.0
	for _, l := range p.Languages {
		best = math.Max(best, languageScore[lang][l])
	}
	s += best
	best = 0
	for _, v := range p.Voices {
		best = math.Max(best, voiceScore[v])
	}
	s += best
	switch {
	case (p.HDR || p.DV) && pr.noHDR:
		s -= 40 // washed out on an SDR screen; outweighs even an explicit 4K choice
	case p.HDR || p.DV:
		s += 3
	}
	if p.Codec == "av1" {
		s -= 5
	}
	s += defaultVocab.Devices[pr.device][p.Codec] // what this device class cannot decode well
	if p.ThreeD {
		s -= 30
	}
	// More seeders stream faster, with diminishing returns: 3 → 8, 30 → 20, 300 → 33.
	return s + 4*math.Log2(float64(seeders)+1)
}
