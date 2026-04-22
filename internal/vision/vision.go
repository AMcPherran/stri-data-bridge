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

// SensorSpec binds a canonical sensor name to the OCR unit string that identifies
// it. Provide an ordered slice to ExtractReadings for charts where multiple sensors
// share the same unit (e.g. two "celsius" rows), so each occurrence is consumed in
// top-to-bottom order rather than deduplicated.
type SensorSpec struct {
	Name    string  // canonical snake_case sensor name
	UnitKey string  // lowercase substring to match in the OCR'd unit string
	Unit    string  // canonical HA unit_of_measurement (e.g. "°C", "W/m²"); overrides OCR unit in readings
	MaxVal  float64 // if > 0 and parsed value exceeds this, divide by 10 repeatedly to recover a dropped decimal point
	Scale   float64 // if > 0, multiply parsed value by this factor before publishing (e.g. 1000 to convert ppt → ppm)
}

// sidebarStartRatio is the fraction of image width where the "Latest Value" column
// begins on STRI charts (~67% puts the left edge right at the value boxes).
const sidebarStartRatio = 0.67

// sidebarEndRatio is the right boundary of the crop. Values beyond ~82% tend to
// be chart decorations (compass rose, station coordinates) that pollute OCR.
const sidebarEndRatio = 0.82

// sidebarTopCropRatio skips the top portion of the cropped sidebar. The first
// ~12% of the image covers the black chart title bar and the "Latest Value"
// header text; removing it lets Tesseract start directly at the value boxes.
const sidebarTopCropRatio = 0.12


// dateLinePattern matches STRI timestamp lines like "22-Apr-2026 12:00" so
// they can be skipped — the regex would otherwise parse them as sensor readings.
var dateLinePattern = regexp.MustCompile(`\d{2}-[A-Za-z]{3}-\d{4}`)

// valuePattern matches "Label: value unit" or "Label - value unit" on one line.
// Group 1 = label, Group 2 = numeric value, Group 3 = unit (optional)
var valuePattern = regexp.MustCompile(`(?i)^([A-Za-z][A-Za-z0-9 _/()-]{1,40})\s*[:\-]\s*([-\d]+(?:\.\d+)?)\s*([°%\w/µ][\w/µ°%]*)?`)

// embeddedPattern finds "number [optional whitespace] unit" substrings anywhere
// in a line. \s* (not \s+) handles units written directly against the number
// (e.g. "78.2%") as well as the more common spaced form ("8.7  km/hr").
var embeddedPattern = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*([A-Za-z°µ%][A-Za-z0-9°µ%/_-]*)`)

// boxValuePattern extracts a reading from a single-box OCR line. Handles:
//   - negative numbers   (-3.4 psi)
//   - comma-formatted    (33,834.0 mg/l)
//   - bare numbers       (8.1 — no unit)
var boxValuePattern = regexp.MustCompile(`(-?[\d,]+(?:\.\d+)?)\s*([A-Za-z°µ%][A-Za-z0-9°µ%/._-]*)?`)

// unitSensor maps lowercased unit substrings to canonical snake_case sensor names.
// Entries are checked in order; first match wins. Garbled OCR variants are included.
// Only used by the generic parser; chart-specific specs take priority.
var unitSensor = []struct{ substr, name string }{
	{"km/h", "wind_speed"},
	{"m/hr", "wind_speed"},
	{"deg", "wind_direction"},
	{"fnu", "turbidity"},
	{"ntu", "turbidity"},
	{"mg/l", "dissolved_oxygen"},
	{"mol", "dissolved_oxygen"},  // "moll" — garbled mg/L
	{"ppt", "salinity"},
	{"psu", "salinity"},
	{"µs/cm", "conductivity"},
	{"us/cm", "conductivity"},
	{"usic", "conductivity"},     // "usicem" — garbled µS/cm
	{"ms/cm", "conductivity"},
	{"µg/l", "chlorophyll"},
	{"ug/l", "chlorophyll"},
	{"mic", "chlorophyll"},       // "Mic-gr/L" — garbled µg/L
	{"w/m", "solar_radiation"},
	{"wim", "solar_radiation"},   // "wim2" — garbled W/m²
	{"celsius", "temperature"},
	{"°c", "temperature"},
	{"mm", "rainfall"},
	{"%", "relative_humidity"},
	{"volts", "battery_voltage"},
	{"volt", "battery_voltage"},
	{"vit", "battery_voltage"},   // "vits" — garbled volts
}

// sensorFromUnit returns the canonical sensor name for a unit string, or "".
func sensorFromUnit(unit string) string {
	lower := strings.ToLower(unit)
	for _, u := range unitSensor {
		if strings.Contains(lower, u.substr) {
			return u.name
		}
	}
	return ""
}

// ExtractReadings crops the right sidebar of a STRI chart PNG, runs Tesseract
// OCR on it, and returns all parseable sensor readings.
//
// When crops is non-empty and matches len(specs), each crop rectangle is taken
// directly from the original image (hardcoded pixel coordinates). This is the
// preferred path for charts with a known, stable layout.
// When crops is empty but specs is non-empty, dynamic box detection is used.
// When specs is empty the generic unit-lookup parser runs on the full sidebar.
func ExtractReadings(imageBytes []byte, chartName string, specs []SensorSpec, crops []image.Rectangle) ([]SensorReading, error) {
	img, err := png.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, fmt.Errorf("decoding PNG for %s: %w", chartName, err)
	}

	cropped := cropSidebar(img)
	enhanced := enhanceContrast(cropped)
	binary := binarizeImage(enhanced, 160)
	scaled := scaleUp2x(binary)

	if os.Getenv("STRI_DEBUG_IMAGES") != "" {
		const debugDir = "/debug"
		if err := os.MkdirAll(debugDir, 0o755); err != nil {
			log.Printf("[vision] debug: cannot create dir %s: %v", debugDir, err)
		} else {
			debugPath := debugDir + "/stri-debug-" + chartName + ".png"
			if f, err := os.Create(debugPath); err != nil {
				log.Printf("[vision] debug: cannot write %s: %v", debugPath, err)
			} else {
				_ = png.Encode(f, scaled)
				f.Close()
				log.Printf("[vision] debug image saved: %s", debugPath)
			}
		}
	}

	if len(crops) == len(specs) && len(specs) > 0 {
		return extractWithCrops(img, chartName, specs, crops)
	}

	if len(specs) > 0 {
		return extractOrdered(binary, chartName, specs)
	}

	// Generic path: OCR the full sidebar image.
	tmpFile, err := os.CreateTemp("", "stri-ocr-*.png")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if err := png.Encode(tmpFile, scaled); err != nil {
		return nil, fmt.Errorf("encoding image: %w", err)
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

	return parseReadings(strings.Split(text, "\n"), chartName), nil
}

// extractWithCrops OCRs each sensor value box using hardcoded pixel rectangles
// in the original image coordinate space. Each box is preprocessed independently
// (enhance → binarize → 2x scale) and fed to Tesseract with PSM_SINGLE_LINE,
// completely isolating it from surrounding chart noise.
func extractWithCrops(img image.Image, chartName string, specs []SensorSpec, crops []image.Rectangle) ([]SensorReading, error) {
	client := gosseract.NewClient()
	defer client.Close()
	client.SetLanguage("eng")
	client.SetPageSegMode(gosseract.PSM_SINGLE_LINE)

	var readings []SensorReading
	for i, crop := range crops {
		boxImg := subImage(img, crop)
		enhanced := enhanceContrast(boxImg)
		binary := binarizeImage(enhanced, 160)
		scaled := scaleUp2x(binary)

		if os.Getenv("STRI_DEBUG_IMAGES") != "" {
			debugPath := fmt.Sprintf("/debug/stri-debug-%s-box%d-%s.png", chartName, i, specs[i].Name)
			if f, err := os.Create(debugPath); err == nil {
				_ = png.Encode(f, scaled)
				f.Close()
			}
		}

		tmpFile, err := os.CreateTemp("", "stri-ocr-*.png")
		if err != nil {
			log.Printf("[vision] %s box[%d] %s: temp file error: %v", chartName, i, specs[i].Name, err)
			continue
		}
		if err := png.Encode(tmpFile, scaled); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			continue
		}
		tmpFile.Close()

		if err := client.SetImage(tmpFile.Name()); err != nil {
			os.Remove(tmpFile.Name())
			continue
		}
		text, err := client.Text()
		os.Remove(tmpFile.Name())
		if err != nil {
			log.Printf("[vision] %s box[%d] %s: OCR error: %v", chartName, i, specs[i].Name, err)
			continue
		}
		line := fixOCRGarbles(strings.TrimSpace(text))
		log.Printf("[vision] %s box[%d] %s: %q", chartName, i, specs[i].Name, line)

		val, ocrUnit, ok := parseBoxValue(line)
		if !ok {
			log.Printf("[vision] %s box[%d] %s: no value parsed from %q", chartName, i, specs[i].Name, line)
			continue
		}
		if specs[i].MaxVal > 0 {
			for val > specs[i].MaxVal && val >= 1 {
				val /= 10
			}
		}
		if specs[i].Scale > 0 {
			val *= specs[i].Scale
		}
		unit := specs[i].Unit
		if unit == "" {
			unit = ocrUnit
		}
		readings = append(readings, SensorReading{
			Name:  specs[i].Name,
			Value: val,
			Unit:  unit,
			Chart: chartName,
		})
	}

	return readings, nil
}

// extractOrdered detects individual value boxes in the binarized sidebar image
// and OCRs each box in isolation with PSM_SINGLE_LINE. Fallback path for
// spec-based charts that do not have hardcoded crop rectangles.
func extractOrdered(binary image.Image, chartName string, specs []SensorSpec) ([]SensorReading, error) {
	boxes := detectBoxes(binary)
	log.Printf("[vision] %s: detected %d box(es) for %d spec(s)", chartName, len(boxes), len(specs))

	// Detect the right edge of the value column to exclude right-panel content
	// that lives in the same crop band (compass rose, station header, etc.).
	colRight := findValueColumnRight(binary)
	log.Printf("[vision] %s: value column right edge at x=%d (crop width=%d)", chartName, colRight, binary.Bounds().Dx())

	client := gosseract.NewClient()
	defer client.Close()
	client.SetLanguage("eng")
	client.SetPageSegMode(gosseract.PSM_SINGLE_LINE)

	// leftPad trims the left vertical border (a few pixels of solid black line).
	const leftPad = 4

	var lines []string
	for i, box := range boxes {
		inner := image.Rect(box.Min.X+leftPad, box.Min.Y, colRight, box.Max.Y)
		if inner.Dx() <= 0 {
			inner = box
		}
		boxImg := subImage(binary, inner)
		scaled := scaleUp2x(boxImg)

		tmpFile, err := os.CreateTemp("", "stri-ocr-*.png")
		if err != nil {
			log.Printf("[vision] temp file error for box %d: %v", i, err)
			lines = append(lines, "")
			continue
		}
		if err := png.Encode(tmpFile, scaled); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			lines = append(lines, "")
			continue
		}
		tmpFile.Close()

		if err := client.SetImage(tmpFile.Name()); err != nil {
			os.Remove(tmpFile.Name())
			lines = append(lines, "")
			continue
		}
		text, err := client.Text()
		os.Remove(tmpFile.Name())
		if err != nil {
			log.Printf("[vision] OCR error for box %d: %v", i, err)
			lines = append(lines, "")
			continue
		}
		lines = append(lines, strings.TrimSpace(text))
	}

	log.Printf("[vision] per-box OCR for %s:\n%s", chartName, strings.Join(lines, "\n"))
	return parseOrdered(lines, chartName, specs), nil
}

// findValueColumnRight scans the image from right to left to locate the right
// vertical border of the value box column. It returns the X coordinate just
// past the border, to be used as the exclusive right bound when cropping boxes.
// Scanning stops at the left-half midpoint as a safety limit.
func findValueColumnRight(img image.Image) int {
	bounds := img.Bounds()
	w := bounds.Max.X - bounds.Min.X
	h := bounds.Max.Y - bounds.Min.Y
	if w == 0 || h == 0 {
		return bounds.Max.X
	}

	// Sample rows at regular intervals across the full height.
	step := h / 20
	if step < 1 {
		step = 1
	}

	// The outermost few pixels may be the image edge or right-panel content;
	// skip them to avoid a false positive right at the crop boundary.
	skip := w / 15

	for x := bounds.Max.X - 1 - skip; x >= bounds.Min.X+w/2; x-- {
		blackCount := 0
		total := 0
		for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
			r, g, b, _ := img.At(x, y).RGBA()
			lum := uint8((19595*r + 38470*g + 7471*b) >> 24)
			if lum < 128 {
				blackCount++
			}
			total++
		}
		if total > 0 && float64(blackCount)/float64(total) >= 0.5 {
			return x + 1 // exclusive — everything to the left of x is inside the box
		}
	}
	return bounds.Max.X - skip
}

// detectBoxes finds the interior regions of value boxes by scanning for
// horizontal rows where the left portion of the image is predominantly black
// (box borders). Returns bounding rectangles of the white interior regions.
func detectBoxes(img image.Image) []image.Rectangle {
	bounds := img.Bounds()
	w := bounds.Max.X - bounds.Min.X
	if w == 0 || bounds.Max.Y <= bounds.Min.Y {
		return nil
	}
	// Scan only the left 40% of the crop — this is the Latest Value column proper
	// and avoids any right-panel content that might also have dark rows.
	checkW := w * 2 / 5
	if checkW < 1 {
		checkW = 1
	}

	const borderFrac = 0.5 // ≥50% black pixels in checkW → border row
	const minBoxH = 8      // minimum interior height in pixels

	inBorder := true
	interiorStart := bounds.Min.Y
	var boxes []image.Rectangle

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		blackCount := 0
		for x := bounds.Min.X; x < bounds.Min.X+checkW; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			lum := uint8((19595*r + 38470*g + 7471*b) >> 24)
			if lum < 128 {
				blackCount++
			}
		}
		isBorder := float64(blackCount)/float64(checkW) >= borderFrac

		if inBorder && !isBorder {
			interiorStart = y
			inBorder = false
		} else if !inBorder && isBorder {
			if y-interiorStart >= minBoxH {
				boxes = append(boxes, image.Rect(bounds.Min.X, interiorStart, bounds.Max.X, y))
			}
			inBorder = true
		}
	}
	// Handle a box that extends to the bottom of the image.
	if !inBorder && bounds.Max.Y-interiorStart >= minBoxH {
		boxes = append(boxes, image.Rect(bounds.Min.X, interiorStart, bounds.Max.X, bounds.Max.Y))
	}
	return boxes
}

// fixOCRGarbles replaces known Tesseract misrecognitions with the correct character.
// § and $ are garbles for "5" seen with this chart's font at low DPI.
func fixOCRGarbles(s string) string {
	s = strings.ReplaceAll(s, "§", "5")
	s = strings.ReplaceAll(s, "$", "5")
	return s
}

// parseBoxValue extracts a (value, unit) pair from a single OCR line produced
// by a hardcoded crop. Unlike the generic embeddedPattern it accepts negative
// numbers, comma-formatted integers, and bare numbers with no unit.
func parseBoxValue(s string) (val float64, unit string, ok bool) {
	m := boxValuePattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil || m[1] == "" {
		return 0, "", false
	}
	numStr := strings.ReplaceAll(m[1], ",", "")
	v, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, "", false
	}
	return v, strings.TrimSpace(m[2]), true
}

// subImage extracts a rectangular region from img into a new *image.Gray.
func subImage(img image.Image, r image.Rectangle) *image.Gray {
	out := image.NewGray(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)
	return out
}

// cropSidebar returns the band of the image between sidebarStartRatio and
// sidebarEndRatio, isolating the "Latest Value" column while excluding the
// compass rose and station-coordinates decorations further to the right.
// The top sidebarTopCropRatio fraction is also removed to eliminate the black
// chart title bar that interferes with Tesseract layout analysis for the first
// value box.
func cropSidebar(img image.Image) image.Image {
	bounds := img.Bounds()
	w := bounds.Max.X - bounds.Min.X
	h := bounds.Max.Y - bounds.Min.Y
	startX := bounds.Min.X + int(float64(w)*sidebarStartRatio)
	endX := bounds.Min.X + int(float64(w)*sidebarEndRatio)
	startY := bounds.Min.Y + int(float64(h)*sidebarTopCropRatio)

	cropW := endX - startX
	cropH := bounds.Max.Y - startY
	cropped := image.NewRGBA(image.Rect(0, 0, cropW, cropH))
	draw.Draw(cropped, cropped.Bounds(), img, image.Pt(startX, startY), draw.Src)
	return cropped
}

// binarizeImage converts to pure black/white. Pixels at or above threshold
// become white; below become black. Assumes dark text on a light background.
func binarizeImage(img image.Image, threshold uint8) image.Image {
	bounds := img.Bounds()
	out := image.NewGray(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			lum := uint8((19595*r + 38470*g + 7471*b) >> 24)
			if lum >= threshold {
				out.SetGray(x, y, color.Gray{Y: 255})
			} else {
				out.SetGray(x, y, color.Gray{Y: 0})
			}
		}
	}
	return out
}

// scaleUp2x doubles the image dimensions using nearest-neighbour interpolation.
// Larger input images significantly improve Tesseract accuracy on small text.
func scaleUp2x(img image.Image) image.Image {
	bounds := img.Bounds()
	w := bounds.Max.X - bounds.Min.X
	h := bounds.Max.Y - bounds.Min.Y
	out := image.NewGray(image.Rect(0, 0, w*2, h*2))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			lum := uint8((19595*r + 38470*g + 7471*b) >> 24)
			px := color.Gray{Y: lum}
			out.SetGray(x*2, y*2, px)
			out.SetGray(x*2+1, y*2, px)
			out.SetGray(x*2, y*2+1, px)
			out.SetGray(x*2+1, y*2+1, px)
		}
	}
	return out
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

type candidate struct {
	val  float64
	unit string
}

// collectCandidates extracts all (value, unit) pairs from OCR lines in reading order.
func collectCandidates(lines []string) []candidate {
	var cands []candidate
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) < 2 || dateLinePattern.MatchString(line) {
			continue
		}
		for _, m := range embeddedPattern.FindAllStringSubmatch(line, -1) {
			val, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				continue
			}
			cands = append(cands, candidate{val, m[2]})
		}
	}
	return cands
}

// normaliseUnit maps common OCR garbles to a canonical form used only for
// unit matching (the original unit string is still stored in the reading).
func normaliseUnit(unit string) string {
	l := strings.ToLower(unit)
	// "W/m²" is commonly garbled as "wim2", "wim?", "win2", "win?" — the slash
	// and superscript are lost and the middle letter varies between 'i' and 'n'.
	if (strings.HasPrefix(l, "wim") || strings.HasPrefix(l, "win")) && len(l) <= 5 {
		return "w/m"
	}
	return l
}

// parseOrdered extracts readings by assigning OCR candidates to specs using
// their positional order in the image.
//
// Specs whose UnitKey is unique across the full spec list (anchor specs) are
// matched first by scanning all candidates. Their OCR positions then define
// the valid candidate range [lo, hi) for each shared-unit spec (specs that
// share a UnitKey with at least one other spec):
//
//   lo = (OCR position of the last anchor spec that precedes this spec) + 1
//   hi = OCR position of the first anchor spec that follows this spec
//
// This prevents a missing sensor value (whose OCR box is unreadable) from
// consuming the next sensor's value and cascading failures. For example, if
// air_temperature (celsius) has no OCR output, its range becomes [0, humidity_pos)
// which is empty, so water_temperature correctly gets the lone celsius candidate.
func parseOrdered(lines []string, chartName string, specs []SensorSpec) []SensorReading {
	cands := collectCandidates(lines)
	n := len(cands)

	// Count how many specs share each UnitKey.
	unitCount := make(map[string]int)
	for _, s := range specs {
		unitCount[s.UnitKey]++
	}

	// Pass 1: for each unique-unit (anchor) spec, find its candidate index.
	anchorPos := make([]int, len(specs)) // -1 = not found or not an anchor
	for i := range anchorPos {
		anchorPos[i] = -1
	}
	for si, spec := range specs {
		if unitCount[spec.UnitKey] == 1 {
			for ci, c := range cands {
				if strings.Contains(normaliseUnit(c.unit), spec.UnitKey) {
					anchorPos[si] = ci
					break
				}
			}
		}
	}

	// Pass 2: emit readings.
	var readings []SensorReading
	lastConsumed := make(map[string]int) // UnitKey → last consumed candidate index

	for si, spec := range specs {
		if unitCount[spec.UnitKey] == 1 {
			// Anchor spec: emit whatever pass 1 found.
			if anchorPos[si] >= 0 {
				c := cands[anchorPos[si]]
				readings = append(readings, SensorReading{
					Name:  spec.Name,
					Value: c.val,
					Unit:  c.unit,
					Chart: chartName,
				})
			}
			continue
		}

		// Shared-unit spec: search within [lo, hi).
		lo := 0
		for j := si - 1; j >= 0; j-- {
			if anchorPos[j] >= 0 {
				lo = anchorPos[j] + 1
				break
			}
		}
		hi := n
		for j := si + 1; j < len(specs); j++ {
			if anchorPos[j] >= 0 {
				hi = anchorPos[j]
				break
			}
		}
		if lc, ok := lastConsumed[spec.UnitKey]; ok && lc+1 > lo {
			lo = lc + 1
		}

		for i := lo; i < hi; i++ {
			c := cands[i]
			if strings.Contains(normaliseUnit(c.unit), spec.UnitKey) {
				readings = append(readings, SensorReading{
					Name:  spec.Name,
					Value: c.val,
					Unit:  c.unit,
					Chart: chartName,
				})
				lastConsumed[spec.UnitKey] = i
				break
			}
		}
	}

	return readings
}

// parseReadings is the generic (non-spec) parser. It uses three strategies:
//
//  1. "Label: value unit" on one line — label must be multi-word to reject OCR garbage.
//  2. Standalone unit line followed by a pure number on the next line.
//  3. Unanchored "number unit" search anywhere in the line.
func parseReadings(lines []string, chartName string) []SensorReading {
	var readings []SensorReading
	seen := make(map[string]bool)

	add := func(name string, val float64, unit string) {
		if name == "" || seen[name] {
			return
		}
		readings = append(readings, SensorReading{
			Name:  name,
			Value: val,
			Unit:  unit,
			Chart: chartName,
		})
		seen[name] = true
	}

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) < 2 {
			continue
		}

		if dateLinePattern.MatchString(line) {
			continue
		}

		// Strategy 1: "Label: value unit" — require space in label to reject
		// single-token OCR garbage (e.g. "i: 0" or "papromnccpteniinnte: 3").
		if m := valuePattern.FindStringSubmatch(line); m != nil {
			rawLabel := strings.TrimSpace(m[1])
			if strings.Contains(rawLabel, " ") {
				if val, err := strconv.ParseFloat(strings.TrimSpace(m[2]), 64); err == nil {
					add(toSnakeCase(rawLabel), val, strings.TrimSpace(m[3]))
					continue
				}
			}
		}

		// Strategy 2: standalone unit line followed by a pure number on the next line.
		if name := sensorFromUnit(line); name != "" && i+1 < len(lines) {
			nextLine := strings.TrimSpace(lines[i+1])
			if val, err := strconv.ParseFloat(nextLine, 64); err == nil {
				add(name, val, line)
			}
		}

		// Strategy 3: unanchored "number unit" search — handles lines where chart
		// rendering artefacts precede the real value.
		for _, em := range embeddedPattern.FindAllStringSubmatch(line, -1) {
			if val, err := strconv.ParseFloat(em[1], 64); err == nil {
				if name := sensorFromUnit(em[2]); name != "" {
					add(name, val, em[2])
				}
			}
		}
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
