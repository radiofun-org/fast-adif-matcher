package main

import "fmt"

func letterToPosition(letter rune, base int) int {
	if base == 18 {
		return int(letter) - int('A')
	} else if base == 24 {
		return int(letter) - int('A')
	}
	return -1 // should never happen
}

func maidenheadToNumber(locator string) int {
	length := len(locator)
	var number int

	L1 := letterToPosition(rune(locator[0]), 18)
	L2 := letterToPosition(rune(locator[1]), 18)

	N1 := int(locator[2] - '0')
	N2 := int(locator[3] - '0')

	if length == 4 {
		number = ((L1*18 + L2) * 100) + (N1*10 + N2) + 1
	} else if length == 6 {
		L3 := letterToPosition(rune(locator[4]), 24)
		L4 := letterToPosition(rune(locator[5]), 24)
		number = ((L1*18 + L2) * 100 * 576) + ((N1*10 + N2) * 576) + (L3*24 + L4 + 1)
	}

	return number
}

func positionToLetter(position int, base int) rune {
	if base == 18 {
		return rune(position + int('A'))
	} else if base == 24 {
		return rune(position + int('A'))
	}
	return '?' // should never happen
}

func numberToMaidenhead(number int, length int) string {
	if length == 4 {
		N2 := (number - 1) % 10
		N1 := ((number - 1) / 10) % 10
		L2 := ((number - 1) / 100) % 18
		L1 := ((number - 1) / (100 * 18)) % 18

		locator := string(positionToLetter(L1, 18)) +
			string(positionToLetter(L2, 18)) +
			fmt.Sprintf("%d%d", N1, N2)

		return locator
	} else if length == 6 {
		L4 := (number - 1) % 24
		L3 := ((number - 1) / 24) % 24
		N2 := ((number - 1) / (24 * 24)) % 10
		N1 := ((number - 1) / (24 * 24 * 10)) % 10
		L2 := ((number - 1) / (24 * 24 * 100)) % 18
		L1 := ((number - 1) / (24 * 24 * 100 * 18)) % 18

		locator := string(positionToLetter(L1, 18)) +
			string(positionToLetter(L2, 18)) +
			fmt.Sprintf("%d%d", N1, N2) +
			string(positionToLetter(L3, 24)) +
			string(positionToLetter(L4, 24))

		return locator
	}

	return "" // should never happen
}
