package capabilities

import "strings"

// NameCharset identifies the characters a derived destination name may use.
type NameCharset string

const (
	// NameCharsetAgentSkill is the Agent Skills rule for a skill name, which
	// must also be the skill's directory name: lowercase ASCII letters, digits
	// and single hyphens, neither leading nor trailing.
	NameCharsetAgentSkill NameCharset = "agent-skill-name"
	// NameCharsetPortableFile is Stemma's conservative subset for a file name
	// stem that must behave the same on every supported filesystem: ASCII
	// letters, digits, '.', '_' and '-', starting with a letter or digit and
	// not ending with '.'.
	NameCharsetPortableFile NameCharset = "portable-file-name"
)

// NameRule constrains one kind of derived destination name.
type NameRule struct {
	Charset NameCharset `json:"charset"`
	// MaxLength bounds the name in bytes, excluding any provider file suffix.
	MaxLength int `json:"maxLength"`
}

// SourceNameHint names an import bookkeeping key in which this provider's
// importer records the original file or directory name of an entity, and the
// provider suffix that the name carries ("" for a directory name).
type SourceNameHint struct {
	Key    string `json:"key"`
	Suffix string `json:"suffix,omitempty"`
	// Nested reports that the recorded name may include subdirectories below
	// the provider's directory; only its last segment is ever reused.
	Nested bool `json:"nested,omitempty"`
}

// Naming describes how a target names the destinations Stemma derives.
type Naming struct {
	// FileNames constrains a source-derived file name stem.
	FileNames NameRule `json:"fileNames"`
	// SkillDirectories constrains a source-derived skill directory name.
	SkillDirectories NameRule `json:"skillDirectories"`
	// SourceNames lists this provider's recorded source names, which other
	// targets may reuse when the name is valid and unambiguous there.
	SourceNames []SourceNameHint `json:"sourceNames"`
}

// AgentSkillNames is the Agent Skills name rule: 1-64 characters from a-z,
// 0-9 and '-', no leading, trailing or consecutive hyphen, equal to the
// parent directory name. Claude Code derives a project skill's command from
// that directory name, so it is also the invocation name.
var AgentSkillNames = NameRule{Charset: NameCharsetAgentSkill, MaxLength: 64}

// PortableFileNames is Stemma's bound for a reused file name stem. No provider
// documents a file-name limit for these files; the bound keeps derived names
// short, ASCII-only and free of shell, glob and Windows-reserved spellings.
var PortableFileNames = NameRule{Charset: NameCharsetPortableFile, MaxLength: 64}

// windowsReserved lists device names that no portable file or directory may
// use as its stem, whatever the extension.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// wellKnownStems lists file names that providers load by name wherever they
// appear. A derived name never takes one of them, in any case, so a reused
// name cannot turn a rule into another provider's instructions file on a
// case-insensitive filesystem.
var wellKnownStems = map[string]bool{
	"agents": true, "agents.override": true, "claude": true, "claude.local": true,
	"copilot-instructions": true, "skill": true,
}

// Allows reports whether name satisfies the rule. It never interprets the
// name as a path: any separator, traversal or special character fails.
func (r NameRule) Allows(name string) bool {
	if name == "" || r.MaxLength <= 0 || len(name) > r.MaxLength {
		return false
	}
	stem, _, _ := strings.Cut(name, ".")
	if windowsReserved[strings.ToLower(stem)] {
		return false
	}
	switch r.Charset {
	case NameCharsetAgentSkill:
		return agentSkillName(name)
	case NameCharsetPortableFile:
		return !wellKnownStems[strings.ToLower(name)] && portableFileName(name)
	default:
		return false
	}
}

func agentSkillName(name string) bool {
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func portableFileName(name string) bool {
	if !alnum(name[0]) || strings.HasSuffix(name, ".") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !alnum(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func alnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
