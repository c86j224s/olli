package cli

import "strings"

const (
	ColorReset   = "\033[0m"
	ColorBold    = "\033[1m"
	ColorDim     = "\033[2m"
	ColorItalic  = "\033[3m"
	ColorCyan    = "\033[36m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorRed     = "\033[31m"
	ColorMagenta = "\033[35m"
	ColorBlue    = "\033[34m"
	ColorWhite   = "\033[37m"
	ColorGray    = "\033[90m"

	// Single-Hue Palette for Main Agent (Cyan Family)
	MainBold   = "\033[1;36m"
	MainNormal = "\033[0;36m"
	MainDim    = "\033[2;36m"
	MainItalic = "\033[3;36m"

	// Single-Hue Palette for Researcher Subagent (Yellow/Amber Family)
	ResBold   = "\033[1;33m"
	ResNormal = "\033[0;33m"
	ResDim    = "\033[2;33m"
	ResItalic = "\033[3;33m"

	// Single-Hue Palette for Coder Subagent (Magenta/Purple Family)
	CoderBold   = "\033[1;35m"
	CoderNormal = "\033[0;35m"
	CoderDim    = "\033[2;35m"
	CoderItalic = "\033[3;35m"

	// Single-Hue Palette for Tester Subagent (Green/Emerald Family)
	TesterBold   = "\033[1;32m"
	TesterNormal = "\033[0;32m"
	TesterDim    = "\033[2;32m"
	TesterItalic = "\033[3;32m"

	// Single-Hue Palette for Reviewer Subagent (Blue Family)
	ReviewerBold   = "\033[1;34m"
	ReviewerNormal = "\033[0;34m"
	ReviewerDim    = "\033[2;34m"
	ReviewerItalic = "\033[3;34m"

	// Single-Hue Palette for Documenter Subagent (Teal Family)
	DocBold   = "\033[1;36m"
	DocNormal = "\033[0;36m"
	DocDim    = "\033[2;36m"
	DocItalic = "\033[3;36m"

	// Single-Hue Palette for Presenter Subagent (Bright White/Pink Family)
	PresBold   = "\033[1;37m"
	PresNormal = "\033[0;37m"
	PresDim    = "\033[2;37m"
	PresItalic = "\033[3;37m"
)

func GetSubagentPalette(subType string) (boldColor string, secondaryColor string) {
	switch strings.ToLower(subType) {
	case "researcher":
		return ResBold, ResItalic
	case "coder":
		return CoderBold, CoderItalic
	case "tester":
		return TesterBold, TesterItalic
	case "reviewer":
		return ReviewerBold, ReviewerItalic
	case "documenter":
		return DocBold, DocItalic
	case "presenter":
		return PresBold, PresItalic
	default:
		return ResBold, ResDim
	}
}
