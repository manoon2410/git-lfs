package filepathfilter

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type Pattern interface {
	// HasPrefix returns whether the receiving Pattern will match a fullpath
	// that contains the prefix "prefix".
	//
	// For instance, if the receiving pattern were to match 'a/b/c.txt',
	// HasPrefix() will return true for:
	//
	//   - 'a', and 'a/'
	//   - 'a/b', and 'a/b/'
	HasPrefix(prefix string) bool

	Match(filename string) bool
	// String returns a string representation (see: regular expressions) of
	// the underlying pattern used to match filenames against this Pattern.
	String() string
}

type Filter struct {
	include []Pattern
	exclude []Pattern
}

func NewFromPatterns(include, exclude []Pattern) *Filter {
	return &Filter{include: include, exclude: exclude}
}

func New(include, exclude []string) *Filter {
	return NewFromPatterns(convertToPatterns(include), convertToPatterns(exclude))
}

// Include returns the result of calling String() on each Pattern in the
// include set of this *Filter.
func (f *Filter) Include() []string { return patternsToStrings(f.include...) }

// Exclude returns the result of calling String() on each Pattern in the
// exclude set of this *Filter.
func (f *Filter) Exclude() []string { return patternsToStrings(f.exclude...) }

// patternsToStrings maps the given set of Pattern's to a string slice by
// calling String() on each pattern.
func patternsToStrings(ps ...Pattern) []string {
	s := make([]string, 0, len(ps))
	for _, p := range ps {
		s = append(s, p.String())
	}

	return s
}

func (f *Filter) Allows(filename string) bool {
	_, allowed := f.AllowsPattern(filename)
	return allowed
}

// HasPrefix returns whether the given prefix "prefix" is a prefix for all
// included Patterns, and not a prefix for any excluded Patterns.
func (f *Filter) HasPrefix(prefix string) bool {
	if f == nil {
		return true
	}

	parts := strings.Split(prefix, sep)

L:
	for i := len(parts); i > 0; i-- {
		prefix := strings.Join(parts[:i], sep)

		for _, p := range f.exclude {
			if p.Match(prefix) {
				break L
			}
		}

		if len(f.include) == 0 {
			return true
		}

		for _, p := range f.include {
			if p.HasPrefix(prefix) {
				return true
			}
		}
	}
	return false
}

// AllowsPattern returns whether the given filename is permitted by the
// inclusion/exclusion rules of this filter, as well as the pattern that either
// allowed or disallowed that filename.
//
// In special cases, such as a nil `*Filter` receiver, the absence of any
// patterns, or the given filename not being matched by any pattern, the empty
// string "" will be returned in place of the pattern.
func (f *Filter) AllowsPattern(filename string) (pattern string, allowed bool) {
	if f == nil {
		return "", true
	}

	if len(f.include)+len(f.exclude) == 0 {
		return "", true
	}

	cleanedName := filepath.Clean(filename)

	if len(f.include) > 0 {
		matched := false
		for _, inc := range f.include {
			matched = inc.Match(cleanedName)
			if matched {
				pattern = inc.String()
				break
			}
		}
		if !matched {
			return "", false
		}
	}

	if len(f.exclude) > 0 {
		for _, ex := range f.exclude {
			if ex.Match(cleanedName) {
				return ex.String(), false
			}
		}
	}

	return pattern, true
}

const (
	sep = string(filepath.Separator)
)

func NewPattern(rawpattern string) Pattern {
	cleanpattern := filepath.Clean(rawpattern)

	// Special case local dir, matches all (inc subpaths)
	if _, local := localDirSet[cleanpattern]; local {
		return &noOpMatcher{pattern: cleanpattern}
	}

	hasPathSep := strings.Contains(cleanpattern, sep)
	ext := filepath.Ext(cleanpattern)
	plen := len(cleanpattern)
	if plen > 1 && !hasPathSep && strings.HasPrefix(cleanpattern, "*") && cleanpattern[1:plen] == ext {
		return &simpleExtPattern{ext: ext}
	}

	// Also support ** with path separators
	if hasPathSep && strings.Contains(cleanpattern, "**") {
		pattern := regexp.QuoteMeta(cleanpattern)
		regpattern := fmt.Sprintf("^%s$", strings.Replace(pattern, "\\*\\*", ".*", -1))
		return &doubleWildcardPattern{
			rawPattern: cleanpattern,
			wildcardRE: regexp.MustCompile(regpattern),
		}
	}

	if strings.Contains(cleanpattern, "*") {
		// special case * when there are no path separators
		// filepath.Match never allows * to match a path separator, which is correct
		// for gitignore IF the pattern includes a path separator, but not otherwise
		// So *.txt should match in any subdir, as should test*, but sub/*.txt would
		// only match directly in the sub dir
		// Don't need to test cross-platform separators as both cleaned above
		if !hasPathSep {
			pattern := regexp.QuoteMeta(cleanpattern)
			regpattern := fmt.Sprintf("^%s$", strings.Replace(pattern, "\\*", ".*", -1))
			return &pathlessWildcardPattern{
				rawPattern: cleanpattern,
				wildcardRE: regexp.MustCompile(regpattern),
			}
		}

		return &pathfulWildcardPattern{
			wildDirs: filepath.SplitList(cleanpattern),
		}
	}

	if hasPathSep && strings.HasPrefix(cleanpattern, sep) {
		rel := cleanpattern[1:len(cleanpattern)]
		prefix := rel
		if strings.HasSuffix(rel, sep) {
			rel = rel[0 : len(rel)-1]
		} else {
			prefix += sep
		}

		return &pathPrefixPattern{
			rawPattern: cleanpattern,
			relative:   rel,
			prefix:     prefix,
		}
	}

	return &pathPattern{
		rawPattern: cleanpattern,
		prefix:     cleanpattern + sep,
		suffix:     sep + cleanpattern,
		inner:      sep + cleanpattern + sep,
	}
}

func convertToPatterns(rawpatterns []string) []Pattern {
	patterns := make([]Pattern, len(rawpatterns))
	for i, raw := range rawpatterns {
		patterns[i] = NewPattern(raw)
	}
	return patterns
}

type pathPrefixPattern struct {
	rawPattern string
	relative   string
	prefix     string
}

// Match is a revised version of filepath.Match which makes it behave more
// like gitignore
func (p *pathPrefixPattern) Match(name string) bool {
	if name == p.relative || strings.HasPrefix(name, p.prefix) {
		return true
	}
	matched, _ := filepath.Match(p.rawPattern, name)
	return matched
}

func (p *pathPrefixPattern) HasPrefix(name string) bool {
	return strings.HasPrefix(p.relative, name)
}

// String returns a string representation of the underlying pattern for which
// this *pathPrefixPattern is matching.
func (p *pathPrefixPattern) String() string {
	return p.rawPattern
}

type pathPattern struct {
	rawPattern string
	prefix     string
	suffix     string
	inner      string
}

// Match is a revised version of filepath.Match which makes it behave more
// like gitignore
func (p *pathPattern) Match(name string) bool {
	if strings.HasPrefix(name, p.prefix) || strings.HasSuffix(name, p.suffix) || strings.Contains(name, p.inner) {
		return true
	}
	matched, _ := filepath.Match(p.rawPattern, name)
	return matched
}

func (p *pathPattern) HasPrefix(name string) bool {
	return strings.HasPrefix(p.prefix, name)
}

// String returns a string representation of the underlying pattern for which
// this *pathPattern is matching.
func (p *pathPattern) String() string {
	return p.rawPattern
}

type simpleExtPattern struct {
	ext string
}

func (p *simpleExtPattern) Match(name string) bool {
	return strings.HasSuffix(name, p.ext)
}

func (p *simpleExtPattern) HasPrefix(name string) bool {
	return true
}

// String returns a string representation of the underlying pattern for which
// this *simpleExtPattern is matching.
func (p *simpleExtPattern) String() string {
	return fmt.Sprintf("*%s", p.ext)
}

// pathfulWildcardPattern matches filepaths with patterns that contain both path
// separators and wildcard operators.
type pathfulWildcardPattern struct {
	wildDirs []string
}

// String implements Pattern.String and returns a string representation of the
// receiving pattern.
func (p *pathfulWildcardPattern) String() string {
	return strings.Join(p.wildDirs, string(filepath.Separator))
}

// HasPrefix returns whether the receiving pattern matches a prefix of the given
// filepath.
func (p *pathfulWildcardPattern) HasPrefix(name string) bool {
	// Prefix occurs when the partial match of the filepath is non-empty.
	return len(p.partial(name)) > 0
}

// Match returns whether the receiving pattern matches the entire filepath.
func (p *pathfulWildcardPattern) Match(name string) bool {
	// Match occurs when the partial match of the filepath "name" is the
	// filepath "name" itself.
	return p.partial(name) == name
}

func (p *pathfulWildcardPattern) partial(name string) string {
	// Since the '*' operator does not match across directories, consider
	// each os.PathSeparator- (or filepath.Separator-) delimited component
	// of the filepath "name".
	splits := filepath.SplitList(name)

	for i, dir := range splits {
		// Within a given directory pattern (i.e., "foo/bar/baz"
		// separates as ("foo", "bar", and "baz"), keep track of the
		// index matched within the component so far.
		var matched int

		// Split everything before the '*' operator and everything after
		// it into a cons cell.
		splits := strings.SplitN(p.wildDirs[i], "*", 2)
		for len(splits) > 0 {
			if !strings.HasPrefix(dir[matched:], splits[0]) {
				// If the prefix of '*' in the pattern does not
				// match the remaining portion of the directory
				// component we are attempting to match, return
				// what we have matched so far.
				return strings.Join(splits[:i], string(filepath.Separator))
			}

			if len(splits) == 2 {
				// If there are two components (i.e., if there
				// is a cdr), then one of two cases remain:

				if strings.Contains(splits[1], "*") {
					// Case (1): The are more '*' operators
					// in the tail to process. Resplit the
					// remaining, and continue to match the
					// remainder of the current directory.
					matched += len(splits[0])
					splits = strings.SplitN(splits[1], "*", 2)
					break
				}

				if !strings.HasSuffix(dir[matched:], splits[1]) {
					// Case (2): There are no more '*'
					// operators, thus ensure that there was
					// a suffix match of the remainder, and
					// return appropriately.
					return strings.Join(splits[:i], string(filepath.Separator))
				}
			}
			break
		}
	}

	// Finally, we either (1) considered no path separators, or (2) we
	// matched the entire path, in which case the subset of the path we
	// matched was the entire path itself.
	return name
}

type pathlessWildcardPattern struct {
	rawPattern string
	wildcardRE *regexp.Regexp
}

// Match is a revised version of filepath.Match which makes it behave more
// like gitignore
func (p *pathlessWildcardPattern) Match(name string) bool {
	matched, _ := filepath.Match(p.rawPattern, name)
	// Match the whole of the base name but allow matching in folders if no path
	return matched || p.wildcardRE.MatchString(filepath.Base(name))
}

func (p *pathlessWildcardPattern) HasPrefix(name string) bool {
	lit, ok := p.wildcardRE.LiteralPrefix()
	if !ok {
		return true
	}

	return strings.HasPrefix(name, lit)
}

// String returns a string representation of the underlying pattern for which
// this *pathlessWildcardPattern is matching.
func (p *pathlessWildcardPattern) String() string {
	return p.rawPattern
}

type doubleWildcardPattern struct {
	rawPattern string
	wildcardRE *regexp.Regexp
}

// Match is a revised version of filepath.Match which makes it behave more
// like gitignore
func (p *doubleWildcardPattern) Match(name string) bool {
	matched, _ := filepath.Match(p.rawPattern, name)
	// Match the whole of the base name but allow matching in folders if no path
	return matched || p.wildcardRE.MatchString(name)
}

func (p *doubleWildcardPattern) HasPrefix(name string) bool {
	lit, ok := p.wildcardRE.LiteralPrefix()
	if !ok {
		return true
	}

	return strings.HasPrefix(name, lit)
}

// String returns a string representation of the underlying pattern for which
// this *doubleWildcardPattern is matching.
func (p *doubleWildcardPattern) String() string {
	return p.rawPattern
}

type noOpMatcher struct {
	pattern string
}

func (n *noOpMatcher) Match(name string) bool {
	return true
}

func (n *noOpMatcher) HasPrefix(name string) bool {
	return true
}

func (n *noOpMatcher) String() string {
	if n == nil {
		return ""
	}
	return n.pattern
}

var localDirSet = map[string]struct{}{
	"*":   struct{}{},
	"*.*": struct{}{},
	".":   struct{}{},
	"./":  struct{}{},
	".\\": struct{}{},
}
