package main

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Release-title parser. The vocabulary is data (vocab.yaml); this file applies it, plus the rules
// that are structure rather than vocabulary: tracker voice codes, seasons, year and the language defaults.

//go:embed vocab.yaml
var vocabYAML []byte

type rule struct {
	Value any      `yaml:"value"`
	Match []string `yaml:"match"`
	Codes []string `yaml:"codes"`
	re    *regexp.Regexp
}

type vocab struct {
	Resolution    []rule                        `yaml:"resolution"`
	Source        []rule                        `yaml:"source"`
	Codec         []rule                        `yaml:"codec"`
	HDR           []string                      `yaml:"hdr"`
	DV            []string                      `yaml:"dv"`
	ThreeD        []string                      `yaml:"threed"`
	Voice         []rule                        `yaml:"voice"`
	Language      []rule                        `yaml:"language"`
	SubsLanguages []string                      `yaml:"subs_languages"`
	Subs          []string                      `yaml:"subs"`
	Original      []string                      `yaml:"original"`
	Junk          []string                      `yaml:"junk"`
	Studios       map[string]string             `yaml:"studios"` // name → language
	Devices       map[string]map[string]float64 `yaml:"devices"` // device class → codec → score

	hdr, dv, threed, subsLangs, subs, orig, junk *regexp.Regexp
	studios                                      []studio
}

type studio struct {
	name, lang, lower string
	re                *regexp.Regexp
}

// RE2's \b only knows ASCII letters, so a Cyrillic token never has one. Every \b in the vocabulary becomes a
// Unicode-aware boundary. It consumes a character, which is fine: patterns are only tested for a match, and the
// one replacement pattern (subs_languages) only needs to remove text.
const boundary = `(?:^|$|[^\p{L}\p{N}])`

func compile(patterns []string) (*regexp.Regexp, error) {
	if len(patterns) == 0 {
		return regexp.MustCompile(`$^`), nil // matches nothing
	}
	return regexp.Compile("(?i)(?:" + strings.ReplaceAll(strings.Join(patterns, "|"), `\b`, boundary) + ")")
}

func loadVocab(data []byte) (*vocab, error) {
	var v vocab
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	var err error
	for _, rs := range [][]rule{v.Resolution, v.Source, v.Codec, v.Voice, v.Language} {
		for i := range rs {
			if rs[i].re, err = compile(rs[i].Match); err != nil {
				return nil, fmt.Errorf("vocab %v: %w", rs[i].Value, err)
			}
		}
	}
	for _, p := range []struct {
		dst **regexp.Regexp
		src []string
	}{{&v.hdr, v.HDR}, {&v.dv, v.DV}, {&v.threed, v.ThreeD}, {&v.subsLangs, v.SubsLanguages}, {&v.subs, v.Subs},
		{&v.orig, v.Original}, {&v.junk, v.Junk}} {
		if *p.dst, err = compile(p.src); err != nil {
			return nil, fmt.Errorf("vocab: %w", err)
		}
	}
	for name, lang := range v.Studios {
		re, err := compile([]string{`\b` + regexp.QuoteMeta(name) + `\b`})
		if err != nil {
			return nil, err
		}
		v.studios = append(v.studios, studio{name: name, lang: lang, lower: strings.ToLower(name), re: re})
	}
	// Longest first, so "Studio X Plus" is found before "Studio X" and the shorter one is then skipped.
	sort.Slice(v.studios, func(a, b int) bool {
		if len(v.studios[a].name) != len(v.studios[b].name) {
			return len(v.studios[a].name) > len(v.studios[b].name)
		}
		return v.studios[a].name < v.studios[b].name
	})
	return &v, nil
}

// defaultVocab is the embedded vocabulary, or the file at GW_VOCAB when set.
var defaultVocab = func() *vocab {
	data := vocabYAML
	if p := os.Getenv("GW_VOCAB"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			panic(fmt.Sprintf("GW_VOCAB: %v", err))
		}
		data = b
	}
	v, err := loadVocab(data)
	if err != nil {
		panic(err)
	}
	return v
}()

// release is what the gateway knows about a release from its title.
type release struct {
	Resolution int      `json:"resolution,omitempty"` // 2160, 1080, 720, 480; 0 = unknown
	Source     string   `json:"source,omitempty"`     // cam, remux, bluray, web-dl, webrip, hdtv, dvd
	Codec      string   `json:"codec,omitempty"`      // av1, hevc, avc
	HDR        bool     `json:"hdr,omitempty"`
	DV         bool     `json:"dv,omitempty"`
	ThreeD     bool     `json:"threed,omitempty"`
	Voices     []string `json:"voices,omitempty"`    // dub, mvo, dvo, avo
	Languages  []string `json:"languages,omitempty"` // audio, ISO 639-1; empty = unknown
	Subs       bool     `json:"subs,omitempty"`
	Original   bool     `json:"original,omitempty"`
	Studios    []string `json:"studios,omitempty"`
	Seasons    []int    `json:"seasons,omitempty"`
	Year       int      `json:"year,omitempty"`
	Junk       bool     `json:"junk,omitempty"` // book, music, game or software
}

var (
	cyrillic  = regexp.MustCompile(`\p{Cyrillic}`)
	ukrainian = regexp.MustCompile(`[іїєґІЇЄҐ]`)
	russian   = regexp.MustCompile(`[ыэъёЫЭЪЁ]`)
	kana      = regexp.MustCompile(`[\p{Hiragana}\p{Katakana}]`)
	fansub    = regexp.MustCompile(`^\[[^\]]+\]`)
	yearRe    = regexp.MustCompile(`(?:^|[^0-9x×])((?:19|20)[0-9]{2})(?:[^0-9px×]|$)`)
	// Cyrillic letters that look like the Latin tracker codes ("D, P, А" typed with a Cyrillic А).
	lookalike = strings.NewReplacer("А", "A", "Р", "P", "В", "B", "Д", "D", "Л", "L")
	seasonRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s([0-9]{1,2})(?:-s?([0-9]{1,2}))?e[0-9]`),                 // S01E02, S1-10E1-214
		regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s([0-9]{1,2})(?:-s?([0-9]{1,2}))?(?:[^a-z0-9]|v[0-9]|$)`), // S01, [S05], S01-S03
		regexp.MustCompile(`(?:^|[^0-9])([0-9]{1,2})x[0-9]{1,3}(?:[^0-9]|$)`),                            // 04x17
		regexp.MustCompile(`(?i)season[ s]*([0-9]{1,2})(?:\s*-\s*([0-9]{1,2}))?`),                        // Season 1-4
		regexp.MustCompile(`(?i)([0-9]{1,2})(?:\s*-\s*([0-9]{1,2}))?\s*(?:сезон|season)`),                // 2 сезон
		regexp.MustCompile(`(?i)сезон[: ]*([0-9]{1,2})(?:\s*-\s*([0-9]{1,2}))?`),                         // Сезон: 3
		regexp.MustCompile(`(?i)(?:тв|tv)-([0-9])`),                                                      // [ТВ-3]
	}
)

func (v *vocab) parse(title string) release {
	var r release
	for _, x := range v.Resolution {
		if x.re.MatchString(title) {
			r.Resolution = toInt(x.Value)
			break
		}
	}
	r.Source = first(v.Source, title)
	r.Codec = first(v.Codec, title)
	r.HDR = v.hdr.MatchString(title)
	r.DV = v.dv.MatchString(title)
	r.ThreeD = v.threed.MatchString(title)
	r.Subs = v.subs.MatchString(title)
	r.Original = v.orig.MatchString(title)
	r.Junk = r.Resolution == 0 && r.Codec == "" && v.junk.MatchString(title) // video never counts as junk

	codes := map[string]bool{}
	if i := strings.IndexByte(title, '|'); i >= 0 {
		for _, seg := range strings.Split(title[i+1:], "|") {
			for _, tok := range strings.Split(seg, ",") {
				tok = strings.TrimSpace(tok)
				if j := strings.IndexByte(tok, '-'); j > 0 { // "D-MovieDalen", "A-Лицензия"
					tok = tok[:j]
				}
				codes[lookalike.Replace(strings.ToUpper(tok))] = true
			}
		}
	}
	for _, x := range v.Voice {
		hit := x.re.MatchString(title)
		for _, c := range x.Codes {
			hit = hit || codes[c]
		}
		if hit {
			r.Voices = append(r.Voices, fmt.Sprint(x.Value))
		}
	}

	audio := v.subsLangs.ReplaceAllString(title, " ")
	langs := map[string]bool{}
	for _, x := range v.Language {
		if x.re.MatchString(audio) {
			langs[fmt.Sprint(x.Value)] = true
		}
	}
	lower := strings.ToLower(title)
	for _, s := range v.studios {
		// The regex only adds word boundaries to a literal name: skip it unless the name is there at all.
		if !strings.Contains(lower, s.lower) || !s.re.MatchString(title) {
			continue
		}
		dup := false
		for _, have := range r.Studios {
			dup = dup || strings.Contains(strings.ToLower(have), strings.ToLower(s.name))
		}
		if !dup {
			r.Studios = append(r.Studios, s.name)
			langs[s.lang] = true
		}
	}
	isCyr := cyrillic.MatchString(title)
	if isCyr && len(r.Voices) > 0 {
		langs["ru"] = true // the voice-over shorthands of Russian-language trackers describe Russian tracks
	}
	if kana.MatchString(title) {
		langs["ja"] = true
	}
	if len(langs) == 0 && !r.Junk {
		switch {
		case isCyr && ukrainian.MatchString(title) && !russian.MatchString(title):
			langs["uk"] = true
		case isCyr:
			langs["ru"] = true // Russian-language trackers carry a Russian track unless they say otherwise
		case fansub.MatchString(title):
			langs["ja"] = true // fansub releases keep the Japanese track
		}
	}
	for l := range langs {
		r.Languages = append(r.Languages, l)
	}
	sort.Strings(r.Languages)
	r.Seasons = seasons(title)
	if m := yearRe.FindStringSubmatch(title); m != nil {
		r.Year, _ = strconv.Atoi(m[1])
	}
	return r
}

func first(rules []rule, title string) string {
	for _, x := range rules {
		if x.re.MatchString(title) {
			return fmt.Sprint(x.Value)
		}
	}
	return ""
}

func seasons(title string) []int {
	for _, re := range seasonRes {
		m := re.FindStringSubmatch(title)
		if m == nil {
			continue
		}
		from, _ := strconv.Atoi(m[1])
		to := from
		if len(m) > 2 && m[2] != "" {
			to, _ = strconv.Atoi(m[2])
		}
		if from < 1 || to < from || to-from > 50 {
			continue
		}
		var out []int
		for s := from; s <= to; s++ {
			out = append(out, s)
		}
		return out
	}
	return nil
}

func toInt(v any) int {
	n, _ := strconv.Atoi(fmt.Sprint(v))
	return n
}
