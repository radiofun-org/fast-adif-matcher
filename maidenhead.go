package main

import (
	"fmt"
	"strings"
)

// Maidenhead grid locators are encoded as a single integer using mixed-radix
// positional notation, stored in the matcher_contact.grid column. The
// matcher_contact.grid_size column stores the original precision (4, 6, 8, 10,
// or 12 characters).
//
// The Maidenhead system uses alternating letter/digit pairs:
//
//	Pair 1 (field):        A-R, A-R    (18 × 18 = 324)
//	Pair 2 (square):       0-9, 0-9    (10 × 10 = 100)
//	Pair 3 (subsquare):    A-X, A-X    (24 × 24 = 576)
//	Pair 4 (ext square):   0-9, 0-9    (10 × 10 = 100)
//	Pair 5 (micro square): A-X, A-X    (24 × 24 = 576)
//	Pair 6 (nano square):  0-9, 0-9    (10 × 10 = 100)
//
// Encoding: n = ((...((pair1 × 100 + pair2) × 576 + pair3) × 100 + pair4) × 576 + pair5) × 100 + pair6
// Result is incremented by 1 so that 0 represents "no grid".
//
// Maximum encoded values and approximate cell size at the equator (lat × lon):
//
//	4 chars:                    32,400    ~111 km  × ~222 km
//	6 chars:                18,662,400    ~4.6 km  × ~9.3 km
//	8 chars:             1,866,240,000    ~463 m   × ~927 m
//	10 chars:        1,075,274,240,000    ~19 m    × ~39 m
//	12 chars:      107,527,424,000,000    ~2 m     × ~4 m

// latLonToMaidenhead converts decimal latitude and longitude to a 6-character
// Maidenhead grid locator.
func latLonToMaidenhead(lat, lon float64) string {
	// Offset so that (−90, −180) maps to (0, 0)
	lon += 180
	lat += 90

	// Field: 18 divisions of 20° lon, 10° lat
	fieldLon := int(lon / 20)
	fieldLat := int(lat / 10)
	lon -= float64(fieldLon) * 20
	lat -= float64(fieldLat) * 10

	// Square: 10 divisions of 2° lon, 1° lat
	sqLon := int(lon / 2)
	sqLat := int(lat / 1)
	lon -= float64(sqLon) * 2
	lat -= float64(sqLat) * 1

	// Subsquare: 24 divisions of 5' lon, 2.5' lat
	subLon := int(lon / (2.0 / 24))
	subLat := int(lat / (1.0 / 24))

	// Clamp to valid ranges
	if subLon > 23 {
		subLon = 23
	}
	if subLat > 23 {
		subLat = 23
	}

	return fmt.Sprintf("%c%c%d%d%c%c",
		'A'+fieldLon, 'A'+fieldLat,
		sqLon, sqLat,
		'a'+subLon, 'a'+subLat,
	)
}

func maidenheadToNumber(locator string) int {
	locator = strings.ToUpper(locator)
	n := int(locator[0]-'A')*18 + int(locator[1]-'A')

	if len(locator) >= 4 {
		n = n*100 + int(locator[2]-'0')*10 + int(locator[3]-'0')
	}

	if len(locator) >= 6 {
		n = n*576 + int(locator[4]-'A')*24 + int(locator[5]-'A')
	}

	if len(locator) >= 8 {
		n = n*100 + int(locator[6]-'0')*10 + int(locator[7]-'0')
	}

	if len(locator) >= 10 {
		n = n*576 + int(locator[8]-'A')*24 + int(locator[9]-'A')
	}

	if len(locator) >= 12 {
		n = n*100 + int(locator[10]-'0')*10 + int(locator[11]-'0')
	}

	return n + 1
}

func numberToMaidenhead(number int, length int) string {
	n := number - 1

	var p6d1, p6d2 int
	if length >= 12 {
		p6d2 = n % 10
		n /= 10
		p6d1 = n % 10
		n /= 10
	}

	var p5l1, p5l2 int
	if length >= 10 {
		p5l2 = n % 24
		n /= 24
		p5l1 = n % 24
		n /= 24
	}

	var p4d1, p4d2 int
	if length >= 8 {
		p4d2 = n % 10
		n /= 10
		p4d1 = n % 10
		n /= 10
	}

	var p3l1, p3l2 int
	if length >= 6 {
		p3l2 = n % 24
		n /= 24
		p3l1 = n % 24
		n /= 24
	}

	p2d2 := n % 10
	n /= 10
	p2d1 := n % 10
	n /= 10

	p1l1 := n / 18
	p1l2 := n % 18

	result := fmt.Sprintf("%c%c%d%d", 'A'+p1l1, 'A'+p1l2, p2d1, p2d2)

	if length >= 6 {
		result += fmt.Sprintf("%c%c", 'A'+p3l1, 'A'+p3l2)
	}

	if length >= 8 {
		result += fmt.Sprintf("%d%d", p4d1, p4d2)
	}

	if length >= 10 {
		result += fmt.Sprintf("%c%c", 'A'+p5l1, 'A'+p5l2)
	}

	if length >= 12 {
		result += fmt.Sprintf("%d%d", p6d1, p6d2)
	}

	return result
}
