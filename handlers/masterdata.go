package handlers

import (
	"fmt"
	"net/http"
	"time"

	"bs-be/models"
	"bs-be/validator"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

// ─────────────────────────────────────────────────────────────
//  MASTER DATA GUIDE
// ─────────────────────────────────────────────────────────────

type masterTableView struct {
	Key         string                 `json:"key"`
	Label       string                 `json:"label"`
	Description string                 `json:"description"`
	UsedBy      []string               `json:"used_by"`
	Items       []models.ReferenceItem `json:"items"`
}

type columnView struct {
	validator.ColumnSpec
	AllowedValues []models.ReferenceItem `json:"allowed_values"`
	RefLabel      string                 `json:"ref_label"`
}

type masterDataSchema struct {
	Columns            []columnView      `json:"columns"`
	Tables             []masterTableView `json:"tables"`
	SupportedMethods   []string          `json:"supported_methods"`
	SupportedDayCounts []string          `json:"supported_day_counts"`
}

// buildMasterDataSchema assembles the full upload contract — every column, its
// format, and the master data values it accepts.
func buildMasterDataSchema() masterDataSchema {
	md := validator.LoadMasterData()

	usedBy := make(map[string][]string)
	columns := make([]columnView, 0, len(validator.InputColumns))
	for _, spec := range validator.InputColumns {
		cv := columnView{ColumnSpec: spec, AllowedValues: []models.ReferenceItem{}}
		if spec.RefTable != "" {
			cv.AllowedValues = md.Items[spec.RefTable]
			cv.RefLabel = validator.MasterTableLabel(spec.RefTable)
			usedBy[spec.RefTable] = append(usedBy[spec.RefTable], spec.Name)
		}
		columns = append(columns, cv)
	}

	tables := make([]masterTableView, 0, len(validator.MasterTables))
	for _, t := range validator.MasterTables {
		items := md.Items[t.Key]
		if items == nil {
			items = []models.ReferenceItem{}
		}
		cols := usedBy[t.Key]
		if cols == nil {
			cols = []string{}
		}
		tables = append(tables, masterTableView{
			Key: t.Key, Label: t.Label, Description: t.Description,
			UsedBy: cols, Items: items,
		})
	}

	return masterDataSchema{
		Columns:            columns,
		Tables:             tables,
		SupportedMethods:   validator.SupportedMethods,
		SupportedDayCounts: validator.SupportedDayCounts,
	}
}

// GetMasterDataSchema returns the upload contract: the columns of the input
// file and every code accepted in each of them. Available to all
// authenticated users — it is what the "how do I build the Excel" guide reads.
func GetMasterDataSchema(c *gin.Context) {
	c.JSON(http.StatusOK, buildMasterDataSchema())
}

// ─────────────────────────────────────────────────────────────
//  EXCEL TEMPLATE
// ─────────────────────────────────────────────────────────────

// DownloadTemplate builds an .xlsx starter file: a "Data" sheet with the exact
// headers the uploader expects plus one filled example row, and a "Master
// Data" sheet listing every valid code with its meaning.
func DownloadTemplate(c *gin.Context) {
	schema := buildMasterDataSchema()

	f := excelize.NewFile()
	defer f.Close()

	const dataSheet = "Data"
	f.SetSheetName(f.GetSheetName(0), dataSheet)

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 10, Color: "#FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#4472C4"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
	})
	requiredStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 10, Color: "#FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#C0504D"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
	})
	hintStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Size: 9, Italic: true, Color: "#5B5B5B"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#F2F2F2"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "top", WrapText: true},
	})

	// Row 1: headers. Row 2: format hint. Row 3: example values.
	for i, col := range schema.Columns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(dataSheet, cell, col.Name)
		if col.Required {
			f.SetCellStyle(dataSheet, cell, cell, requiredStyle)
		} else {
			f.SetCellStyle(dataSheet, cell, cell, headerStyle)
		}

		hintCell, _ := excelize.CoordinatesToCellName(i+1, 2)
		f.SetCellValue(dataSheet, hintCell, templateHint(col))
		f.SetCellStyle(dataSheet, hintCell, hintCell, hintStyle)

		exampleCell, _ := excelize.CoordinatesToCellName(i+1, 3)
		f.SetCellValue(dataSheet, exampleCell, col.Example)

		colName, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(dataSheet, colName, colName, 22)
	}
	f.SetRowHeight(dataSheet, 2, 46)
	f.SetPanes(dataSheet, &excelize.Panes{
		Freeze: true, Split: false, XSplit: 0, YSplit: 2,
		TopLeftCell: "A3", ActivePane: "bottomLeft",
	})

	// Dropdown validation on every coded column, sourced from Master Data.
	addTemplateDropdowns(f, dataSheet, schema)

	// ── Master Data sheet ────────────────────────────────────
	const refSheet = "Master Data"
	f.NewSheet(refSheet)
	writeMasterDataSheet(f, refSheet, schema)

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf(
		`attachment; filename="betterbankings_upload_template_%s.xlsx"`,
		time.Now().Format("20060102"),
	))
	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build template"})
	}
}

func templateHint(col columnView) string {
	prefix := "OPTIONAL"
	if col.Required {
		prefix = "REQUIRED"
	}
	hint := prefix + " · " + col.Format
	if len(col.AllowedValues) > 0 {
		hint += "\nCodes: "
		for i, item := range col.AllowedValues {
			if i > 0 {
				hint += ", "
			}
			hint += item.ID + " = " + item.Name
		}
	}
	return hint
}

// addTemplateDropdowns attaches an in-cell dropdown to each coded column so
// the person filling the template picks a valid code instead of typing one.
func addTemplateDropdowns(f *excelize.File, sheet string, schema masterDataSchema) {
	for i, col := range schema.Columns {
		if len(col.AllowedValues) == 0 {
			continue
		}
		colName, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			continue
		}
		values := make([]string, 0, len(col.AllowedValues))
		for _, item := range col.AllowedValues {
			values = append(values, item.ID)
		}
		dv := excelize.NewDataValidation(true)
		dv.Sqref = fmt.Sprintf("%s3:%s5000", colName, colName)
		if err := dv.SetDropList(values); err != nil {
			continue // list too long for an inline dropdown; the hint row still documents it
		}
		dv.SetError(excelize.DataValidationErrorStyleStop,
			"Not in master data",
			fmt.Sprintf("%s only accepts: %s", col.Name, joinItems(col.AllowedValues)))
		f.AddDataValidation(sheet, dv)
	}
}

func joinItems(items []models.ReferenceItem) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += ", "
		}
		out += it.ID + " = " + it.Name
	}
	return out
}

func writeMasterDataSheet(f *excelize.File, sheet string, schema masterDataSchema) {
	titleStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 12, Color: "#1F3864"},
	})
	headStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 10, Color: "#FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#4472C4"}, Pattern: 1},
	})
	noteStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Size: 9, Italic: true, Color: "#5B5B5B"},
	})
	codeStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Color: "#C0504D"},
	})

	f.SetColWidth(sheet, "A", "A", 12)
	f.SetColWidth(sheet, "B", "B", 34)
	f.SetColWidth(sheet, "C", "C", 60)

	row := 1
	set := func(col string, v interface{}, style int) {
		cell := fmt.Sprintf("%s%d", col, row)
		f.SetCellValue(sheet, cell, v)
		if style != 0 {
			f.SetCellStyle(sheet, cell, cell, style)
		}
	}

	set("A", "Master Data — the codes accepted by the Data sheet", titleStyle)
	row++
	set("A", "Write the CODE (left column) into the Data sheet, not the name. Codes are managed in Admin → Master Data.", noteStyle)
	row += 2

	for _, t := range schema.Tables {
		set("A", t.Label, titleStyle)
		row++
		if t.Description != "" {
			set("A", t.Description, noteStyle)
			row++
		}
		if len(t.UsedBy) > 0 {
			used := "Used by column(s): "
			for i, c := range t.UsedBy {
				if i > 0 {
					used += ", "
				}
				used += c
			}
			set("A", used, noteStyle)
			row++
		}

		set("A", "Code", headStyle)
		set("B", "Meaning", headStyle)
		row++

		if len(t.Items) == 0 {
			set("A", "(empty — ask a SuperAdmin to add entries)", noteStyle)
			row++
		}
		for _, item := range t.Items {
			set("A", item.ID, codeStyle)
			set("B", item.Name, 0)
			row++
		}
		row++
	}
}
