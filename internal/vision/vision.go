package vision

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/otiai10/gosseract/v2"
)

// SensorReading holds a single parsed sensor value extracted from a chart.
type SensorReading struct {
	Name  string
	Value float64
	Unit  string
	Chart string
}

// sidebarRatio is the fraction of image width where the right sidebar begins.
// The STRI charts show current values in the rightmost ~25% of the image.
// Tune this constant if OCR yields poor results on the actual live images.
const sidebarRatio = 0.72

// valuePattern matches lines like:
//
//	Air Temperature: 27.3 °C
//	Dissolved Oxygen: 6.85 mg/L
//	Sea Level : -0.12 m
//
// Group 1 = label, Group 2 = numeric value, Group 3 = unit (optional)
var valuePattern = regexp.MustCompile(`(?i)^([A-Za-z][A-Za-z0-9 _/()-]{1,40})\s*[:\-]\s*([-\d]+(?:\.\d+)?)\s*([°%\w/µ][\w/µ°%]*)?`)

// ExtractReadings crops the right sidebar of a STRI chart PNG, runs Tesseract
// OCR on it, and returns all parseable sensor readings.
func ExtractReadings(imageBytes []byte, chartName string) ([]SensorReading, error) {
	img, err := png.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, fmt.Errorf("decoding PNG for %s: %w", chartName, err)
	}

	cropped := cropSidebar(img)
	enhanced := enhanceContrast(cropped)

	tmpFile, err := os.CreateTemp("", "stri-ocr-*.png")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if err := png.Encode(tmpFile, enhanced); err != nil {
		return nil, fmt.Errorf("encoding cropped image: %w", err)
	}
	tmpFile.Close()

	client := gosseract.NewClient()
	defer client.Close()

	if err := client.SetImage(tmpFile.Name()); err != nil {
		return nil, fmt.Errorf("setting tesseract image: %w", err)
	}
	client.SetLanguage("eng")
	client.SetPageSegMode(gosseract.PSM_SINGLE_BLOCK)

	text, err := client.Text()
	if err != nil {
		return nil, fmt.Errorf("tesseract OCR failed for %s: %w", chartName, err)
	}

	log.Printf("[vision] raw OCR for %s:\n%s", chartName, text)

	return parseReadings(text, chartName), nil
}

// cropSidebar returns the rightmost sidebarRatio portion of the image.
func cropSidebar(img image.Image) image.Image {
	bounds := img.Bounds()
	w := bounds.Max.X - bounds.Min.X
	cropX := bounds.Min.X + int(float64(w)*sidebarRatio)

	cropped := image.NewRGBA(image.Rect(0, 0, bounds.Max.X-cropX, bounds.Max.Y-bounds.Min.Y))
	draw.Draw(cropped, cropped.Bounds(), img, image.Pt(cropX, bounds.Min.Y), draw.Src)
	return cropped
}

// enhanceContrast converts to grayscale and applies linear contrast stretching
// so faint text becomes more legible for Tesseract.
func enhanceContrast(img image.Image) image.Image {
	bounds := img.Bounds()
	out := image.NewGray(bounds)

	var minL, maxL uint32 = math.MaxUint32, 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			// Luminance (0–65535 range)
			lum := (19595*r + 38470*g + 7471*b) >> 16
			if lum < minL {
				minL = lum
			}
			if lum > maxL {
				maxL = lum
			}
		}
	}

	rangeL := maxL - minL
	if rangeL == 0 {
		rangeL = 1
	}

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			lum := (19595*r + 38470*g + 7471*b) >> 16
			stretched := uint8((lum-minL) * 255 / rangeL)
			out.SetGray(x, y, color.Gray{Y: stretched})
		}
	}
	return out
}

// parseReadings scans OCR text line by line and extracts sensor name/value/unit triples.
func parseReadings(text, chartName string) []SensorReading {
	var readings []SensorReading
	seen := make(map[string]bool)

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 4 {
			continue
		}

		m := valuePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		name := toSnakeCase(strings.TrimSpace(m[1]))
		if name == "" || seen[name] {
			continue
		}

		val, err := strconv.ParseFloat(strings.TrimSpace(m[2]), 64)
		if err != nil {
			continue
		}

		unit := strings.TrimSpace(m[3])

		readings = append(readings, SensorReading{
			Name:  name,
			Value: val,
			Unit:  unit,
			Chart: chartName,
		})
		seen[name] = true
	}
	return readings
}

// toSnakeCase converts a label like "Air Temperature" → "air_temperature".
func toSnakeCase(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if prevSpace && b.Len() > 0 {
				b.WriteRune('_')
			}
			b.WriteRune(unicode.ToLower(r))
			prevSpace = false
		} else {
			prevSpace = true
		}
	}
	return b.String()
}
