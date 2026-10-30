// Package xlsxgrid reads the cells of a worksheet in an Office Open XML
// workbook (.xlsx) as text: enough to compare a table a standard publishes
// as a spreadsheet with pdf0's encoding of it. It reads values only —
// shared strings, inline strings and plain values — and no formatting.
package xlsxgrid

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Cell is a cell's position, row and column both from 1.
type Cell struct{ Row, Col int }

// Sheet reads the worksheet named name, or the first worksheet when name is
// "", from the workbook xlsx.
func Sheet(xlsx []byte, name string) (map[Cell]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(xlsx), int64(len(xlsx)))
	if err != nil {
		return nil, err
	}
	read := func(p string) ([]byte, error) {
		f, err := zr.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(f)
	}
	target, err := sheetPath(read, name)
	if err != nil {
		return nil, err
	}
	shared, err := sharedStrings(read)
	if err != nil {
		return nil, err
	}
	data, err := read(target)
	if err != nil {
		return nil, err
	}
	var sheet struct {
		Rows []struct {
			C []struct {
				R  string `xml:"r,attr"`
				T  string `xml:"t,attr"`
				V  string `xml:"v"`
				IS struct {
					T []string `xml:"t"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(data, &sheet); err != nil {
		return nil, err
	}
	ref := regexp.MustCompile(`^([A-Z]+)(\d+)$`)
	grid := map[Cell]string{}
	for _, row := range sheet.Rows {
		for _, c := range row.C {
			m := ref.FindStringSubmatch(c.R)
			if m == nil {
				continue
			}
			col := 0
			for _, ch := range m[1] {
				col = col*26 + int(ch-'A'+1)
			}
			r, _ := strconv.Atoi(m[2])
			var v string
			switch c.T {
			case "s":
				i, err := strconv.Atoi(c.V)
				if err != nil || i < 0 || i >= len(shared) {
					return nil, fmt.Errorf("xlsxgrid: cell %s: shared string %q out of range", c.R, c.V)
				}
				v = shared[i]
			case "inlineStr":
				v = strings.Join(c.IS.T, "")
			default:
				v = c.V
			}
			if v != "" {
				grid[Cell{r, col}] = v
			}
		}
	}
	return grid, nil
}

// sheetPath finds the part that holds the worksheet named name (the first
// when name is ""), through the workbook and its relationships.
func sheetPath(read func(string) ([]byte, error), name string) (string, error) {
	wb, err := read("xl/workbook.xml")
	if err != nil {
		return "", err
	}
	var book struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(wb, &book); err != nil {
		return "", err
	}
	rid := ""
	for _, s := range book.Sheets {
		if name == "" || s.Name == name {
			rid = s.RID
			break
		}
	}
	if rid == "" {
		return "", fmt.Errorf("xlsxgrid: no worksheet named %q", name)
	}
	rels, err := read("xl/_rels/workbook.xml.rels")
	if err != nil {
		return "", err
	}
	var rs struct {
		R []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(rels, &rs); err != nil {
		return "", err
	}
	for _, r := range rs.R {
		if r.ID == rid {
			if strings.HasPrefix(r.Target, "/") {
				return strings.TrimPrefix(r.Target, "/"), nil
			}
			return path.Join("xl", r.Target), nil
		}
	}
	return "", fmt.Errorf("xlsxgrid: worksheet %q has no part", name)
}

// sharedStrings reads the workbook's shared string table; a workbook
// without one has none.
func sharedStrings(read func(string) ([]byte, error)) ([]string, error) {
	data, err := read("xl/sharedStrings.xml")
	if err != nil {
		return nil, nil
	}
	var sst struct {
		SI []struct {
			T []string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if err := xml.Unmarshal(data, &sst); err != nil {
		return nil, err
	}
	out := make([]string, len(sst.SI))
	for i, si := range sst.SI {
		var b strings.Builder
		for _, s := range si.T {
			b.WriteString(s)
		}
		for _, r := range si.R {
			b.WriteString(r.T)
		}
		out[i] = b.String()
	}
	return out, nil
}
