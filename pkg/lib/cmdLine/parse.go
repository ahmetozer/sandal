package cmdline

import (
	"strings"
	"unicode"
)

func Parse(cmdLine string) []string {
	var args []string
	var currentArg strings.Builder
	inQuotes := false
	inSingleQuotes := false
	escaped := false
	lastWasSpace := true // Track if last character was space

	for i, char := range cmdLine {
		switch {
		case escaped:
			// Handle escaped characters
			if char == 'n' {
				currentArg.WriteRune('\n')
			} else if char == 't' {
				currentArg.WriteRune('\t')
			} else {
				currentArg.WriteRune(char)
			}
			escaped = false
			lastWasSpace = false

		case char == '\\':
			// Check if it's end of string
			if i == len(cmdLine)-1 {
				currentArg.WriteRune(char)
			} else {
				escaped = true
			}
			lastWasSpace = false

		case char == '"' && !inSingleQuotes:
			// Handle empty quoted strings
			if lastWasSpace && !inQuotes && i < len(cmdLine)-1 && cmdLine[i+1] == '"' {
				args = append(args, "")
				i++ // Skip next quote
			} else {
				inQuotes = !inQuotes
			}
			lastWasSpace = false

		case char == '\'' && !inQuotes:
			// Handle empty single-quoted strings
			if lastWasSpace && !inSingleQuotes && i < len(cmdLine)-1 && cmdLine[i+1] == '\'' {
				args = append(args, "")
				i++ // Skip next quote
			} else {
				inSingleQuotes = !inSingleQuotes
			}
			lastWasSpace = false

		case unicode.IsSpace(char) && !inQuotes && !inSingleQuotes:
			if currentArg.Len() > 0 {
				args = append(args, currentArg.String())
				currentArg.Reset()
			}
			lastWasSpace = true

		default:
			currentArg.WriteRune(char)
			lastWasSpace = false
		}
	}

	// Handle unclosed quotes
	if currentArg.Len() > 0 {
		args = append(args, currentArg.String())
	}

	return args
}
