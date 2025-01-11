package wordgenerator

import (
	"math/rand/v2"
	"strings"
)

const (
	vowels         = "aeiou"
	consonants     = "bcdfgjklmnpqstvxz"
	semiConsonants = "hrwy"
	// numerals = "0123456789"
	// symbols  = "~!@#$%^&*()-_+={}[]\\|<,>.?/\"';:`"
	// ascii     = lowerCase + upperCase + numerals + symbols
)

type letterType uint8

const (
	vowel         letterType = 1
	consonant     letterType = 2
	semiConsonant letterType = 3
)

// func selectLetterType(letters []letter) letterType {
func selectLetterType(v, c, s float64) letterType {

	// Generate a random number between 0 and 1
	randomNumber := rand.Float64()

	cumulativeProbability := 0.0
	cumulativeProbability += v
	if randomNumber <= cumulativeProbability {
		return vowel
	}

	cumulativeProbability += c
	if randomNumber <= cumulativeProbability {
		return consonant
	}
	cumulativeProbability += s
	if randomNumber <= cumulativeProbability {
		return semiConsonant
	}

	// Default case, should not occur if probabilities are correctly set to sum up to 1
	return 0

}

func getRandomCharacter(input string) string {
	// Get the length of the string
	length := len(input)

	// Generate a random index
	randomIndex := rand.IntN(length)

	// Return the character at the random index as a string
	return string(input[randomIndex])
}
func unsafeWordGenerate(lenght int) []string {
	defer func() {
		if err := recover(); err != nil {

		}
	}()
	var letterTypes []letterType
	letterTypes = append(letterTypes, selectLetterType(0.33, 0.33, 0.33))

	for i := 1; i < lenght; i++ {
		vowelRatio := 1.0
		if i > 2 {
			if letterTypes[i-1] != vowel && letterTypes[i-2] != vowel {
				vowelRatio = 2.5
			}
		}

		if i == 1 || i == lenght {
			if letterTypes[i-1] == vowel {
				vowelRatio = 0.1
			} else {
				vowelRatio = 10
			}
		}

		switch letterTypes[i-1] {
		case vowel:
			letterTypes = append(letterTypes, selectLetterType(0.10*vowelRatio, 0.35/vowelRatio, 0.55/vowelRatio))
		case consonant:
			letterTypes = append(letterTypes, selectLetterType(0.60*vowelRatio, 0.23/vowelRatio, 0.17/vowelRatio))
		case semiConsonant:
			letterTypes = append(letterTypes, selectLetterType(0.70*vowelRatio, 0.20/vowelRatio, 0.10/vowelRatio))
		}
	}

	s := []string{}
	for _, v := range letterTypes {
		var char string
		switch v {
		case vowel:
			char = getRandomCharacter(vowels)
		case consonant:
			char = getRandomCharacter(consonants)
		case semiConsonant:
			char = getRandomCharacter(semiConsonants)
		}
		s = append(s, char)
	}

	return s
}
func WordGenerate(lenght int) []string {
	// try 5 times if fails
	for i := 1; i < 5; i++ {
		if word := unsafeWordGenerate(lenght); word != nil {
			return word
		}
	}
	return []string{}
}

func NameGenerate(lenght int) []string {
	mid := lenght / 2
	offset := rand.IntN((mid / 2))

	return []string{strings.Join(WordGenerate(mid-offset), ""), strings.Join(WordGenerate(mid+offset), "")}
}
