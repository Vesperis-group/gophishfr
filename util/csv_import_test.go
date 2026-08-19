package util

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Vesperis-group/gophishfr/models"
)

// buildImportRequest returns a multipart request shaped exactly like the one the
// group import UI sends: one or more "files[]" parts holding CSV content.
func buildImportRequest(t *testing.T, files map[string]string) *http.Request {
	t.Helper()
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for name, content := range files {
		part, err := writer.CreateFormFile("files[]", name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/api/import/group", body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func parseImportRequest(t *testing.T, files map[string]string) ([]models.Target, error) {
	t.Helper()
	return ParseCSV(buildImportRequest(t, files))
}

// TestParseCSVAcceptsSupportedContent locks the import contract that existed
// before the limits were introduced.
func TestParseCSVAcceptsSupportedContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []models.BaseRecipient
	}{
		{
			name:    "minimal csv",
			content: "First Name,Last Name,Email\nJohn,Doe,john.doe@example.invalid\n",
			want: []models.BaseRecipient{
				{FirstName: "John", LastName: "Doe", Email: "john.doe@example.invalid"},
			},
		},
		{
			name:    "several records with position",
			content: "First Name,Last Name,Email,Position\nJohn,Doe,john.doe@example.invalid,Analyst\nJane,Roe,jane.roe@example.invalid,Manager\n",
			want: []models.BaseRecipient{
				{FirstName: "John", LastName: "Doe", Email: "john.doe@example.invalid", Position: "Analyst"},
				{FirstName: "Jane", LastName: "Roe", Email: "jane.roe@example.invalid", Position: "Manager"},
			},
		},
		{
			name:    "utf-8 values",
			content: "First Name,Last Name,Email\nZoé,Lefèvre-Ångström,zoe@example.invalid\n",
			want: []models.BaseRecipient{
				{FirstName: "Zoé", LastName: "Lefèvre-Ångström", Email: "zoe@example.invalid"},
			},
		},
		{
			name:    "utf-8 byte order mark",
			content: "\xef\xbb\xbfFirst Name,Last Name,Email\nJohn,Doe,john.doe@example.invalid\n",
			want: []models.BaseRecipient{
				{FirstName: "John", LastName: "Doe", Email: "john.doe@example.invalid"},
			},
		},
		{
			name:    "carriage return line endings",
			content: "First Name,Last Name,Email\r\nJohn,Doe,john.doe@example.invalid\r\n",
			want: []models.BaseRecipient{
				{FirstName: "John", LastName: "Doe", Email: "john.doe@example.invalid"},
			},
		},
		{
			name:    "angle bracketed address",
			content: "First Name,Last Name,Email\nJohn,Doe,<john.doe@example.invalid>\n",
			want: []models.BaseRecipient{
				{FirstName: "John", LastName: "Doe", Email: "john.doe@example.invalid"},
			},
		},
		{
			name:    "alternate header spelling and order",
			content: "email,first_name,LAST NAME\njohn.doe@example.invalid,John,Doe\n",
			want: []models.BaseRecipient{
				{FirstName: "John", LastName: "Doe", Email: "john.doe@example.invalid"},
			},
		},
		{
			name:    "ragged records keep being accepted",
			content: "First Name,Last Name,Email\nJohn,Doe,john.doe@example.invalid,extra\nJane,Roe,jane.roe@example.invalid\n",
			want: []models.BaseRecipient{
				{FirstName: "John", LastName: "Doe", Email: "john.doe@example.invalid"},
				{FirstName: "Jane", LastName: "Roe", Email: "jane.roe@example.invalid"},
			},
		},
		{
			name:    "rows without a usable address are skipped",
			content: "First Name,Last Name,Email\nJohn,Doe,john.doe@example.invalid\nJane,Roe,not-an-address\n",
			want: []models.BaseRecipient{
				{FirstName: "John", LastName: "Doe", Email: "john.doe@example.invalid"},
			},
		},
		{
			name:    "header only",
			content: "First Name,Last Name,Email\n",
			want:    []models.BaseRecipient{},
		},
		{
			name:    "unrelated header is ignored",
			content: "column a,column b\nvalue a,value b\n",
			want:    []models.BaseRecipient{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseImportRequest(t, map[string]string{"targets.csv": test.content})
			if err != nil {
				t.Fatalf("ParseCSV() error = %v", err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("ParseCSV() returned %d targets, want %d: %+v", len(got), len(test.want), got)
			}
			for i, want := range test.want {
				if got[i].BaseRecipient != want {
					t.Fatalf("target %d = %+v, want %+v", i, got[i].BaseRecipient, want)
				}
			}
		})
	}
}

// TestParseCSVAcceptsTextFileExtension documents that the uploaded file name is
// metadata: the content decides how the upload is parsed.
func TestParseCSVAcceptsTextFileExtension(t *testing.T) {
	got, err := parseImportRequest(t, map[string]string{
		"../../etc/passwd.txt": "First Name,Last Name,Email\nJohn,Doe,john.doe@example.invalid\n",
	})
	if err != nil {
		t.Fatalf("ParseCSV() error = %v", err)
	}
	if len(got) != 1 || got[0].Email != "john.doe@example.invalid" {
		t.Fatalf("ParseCSV() = %+v, want a single parsed target", got)
	}
}

func TestParseCSVAcceptsSeveralFiles(t *testing.T) {
	got, err := parseImportRequest(t, map[string]string{
		"a.csv": "First Name,Last Name,Email\nJohn,Doe,john.doe@example.invalid\n",
		"b.csv": "First Name,Last Name,Email\nJane,Roe,jane.roe@example.invalid\n",
	})
	if err != nil {
		t.Fatalf("ParseCSV() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ParseCSV() returned %d targets, want 2", len(got))
	}
}

// TestParseCSVWithoutFilePart keeps the historical response for a body that
// carries no upload: an empty target list rather than an error.
func TestParseCSVWithoutFilePart(t *testing.T) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("submit", "1"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/api/import/group", body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	got, err := ParseCSV(request)
	if err != nil {
		t.Fatalf("ParseCSV() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ParseCSV() returned %d targets, want 0", len(got))
	}
}

func TestParseCSVRejectsTooManyFiles(t *testing.T) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for i := 0; i <= MaxImportFiles; i++ {
		part, err := writer.CreateFormFile("files[]", fmt.Sprintf("targets-%d.csv", i))
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fmt.Fprintf(part, "First Name,Last Name,Email\nUser%d,Test,user%d@example.invalid\n", i, i); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/api/import/group", body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	if _, err := ParseCSV(request); !errors.Is(err, ErrCSVTooManyFiles) {
		t.Fatalf("ParseCSV() error = %v, want %v", err, ErrCSVTooManyFiles)
	}
}

func TestParseCSVRejectsTooManyParts(t *testing.T) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for i := 0; i <= MaxImportParts; i++ {
		if err := writer.WriteField(fmt.Sprintf("field-%d", i), "value"); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/api/import/group", body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	if _, err := ParseCSV(request); !errors.Is(err, ErrCSVTooManyParts) {
		t.Fatalf("ParseCSV() error = %v, want %v", err, ErrCSVTooManyParts)
	}
}

func buildRecords(count int) string {
	content := new(strings.Builder)
	content.WriteString("First Name,Last Name,Email\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(content, "User%d,Test,user%d@example.invalid\n", i, i)
	}
	return content.String()
}

func TestParseCSVRecordLimitBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		records int
		wantErr error
	}{
		{name: "below the limit", records: MaxImportRecords - 1},
		{name: "at the limit", records: MaxImportRecords},
		{name: "above the limit", records: MaxImportRecords + 1, wantErr: ErrCSVTooManyRecords},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseImportRequest(t, map[string]string{"targets.csv": buildRecords(test.records)})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("ParseCSV() error = %v, want %v", err, test.wantErr)
				}
				if len(got) > MaxImportRecords {
					t.Fatalf("ParseCSV() returned %d targets, want at most %d", len(got), MaxImportRecords)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCSV() error = %v", err)
			}
			if len(got) != test.records {
				t.Fatalf("ParseCSV() returned %d targets, want %d", len(got), test.records)
			}
		})
	}
}

// TestParseCSVRecordLimitSpansFiles ensures the record budget covers the whole
// request rather than being reset by every uploaded file.
func TestParseCSVRecordLimitSpansFiles(t *testing.T) {
	got, err := parseCSVTargets(strings.NewReader(buildRecords(3)), 2)
	if !errors.Is(err, ErrCSVTooManyRecords) {
		t.Fatalf("parseCSVTargets() error = %v, want %v", err, ErrCSVTooManyRecords)
	}
	if len(got) != 2 {
		t.Fatalf("parseCSVTargets() returned %d targets, want 2", len(got))
	}
}

func TestParseCSVFieldLengthBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		length  int
		wantErr error
	}{
		{name: "at the limit", length: MaxImportFieldCharacters},
		{name: "above the limit", length: MaxImportFieldCharacters + 1, wantErr: ErrCSVFieldTooLong},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := fmt.Sprintf(
				"First Name,Last Name,Email\n%s,Doe,john.doe@example.invalid\n",
				strings.Repeat("a", test.length),
			)
			got, err := parseImportRequest(t, map[string]string{"targets.csv": content})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("ParseCSV() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCSV() error = %v", err)
			}
			if len(got) != 1 || len([]rune(got[0].FirstName)) != test.length {
				t.Fatalf("ParseCSV() = %+v, want one target with a %d character name", got, test.length)
			}
		})
	}
}

// TestParseCSVFieldLimitCountsCharacters keeps multi-byte values that fit the
// database columns from being rejected as if they were byte strings.
func TestParseCSVFieldLimitCountsCharacters(t *testing.T) {
	content := fmt.Sprintf(
		"First Name,Last Name,Email\n%s,Doe,john.doe@example.invalid\n",
		strings.Repeat("é", MaxImportFieldCharacters),
	)
	got, err := parseImportRequest(t, map[string]string{"targets.csv": content})
	if err != nil {
		t.Fatalf("ParseCSV() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ParseCSV() returned %d targets, want 1", len(got))
	}
}

// TestParseCSVNeverTruncates proves an oversized value is reported instead of
// being silently shortened to fit.
// TestParseCSVFieldLimitOnlyRejectsImportedRecords keeps a record that is
// skipped for an unusable address from failing the whole upload because one of
// its other fields is too long.
func TestParseCSVFieldLimitOnlyRejectsImportedRecords(t *testing.T) {
	content := fmt.Sprintf(
		"First Name,Last Name,Email\n%s,Doe,not-an-address\nJane,Roe,jane.roe@example.invalid\n",
		strings.Repeat("a", MaxImportFieldCharacters+1),
	)
	got, err := parseImportRequest(t, map[string]string{"targets.csv": content})
	if err != nil {
		t.Fatalf("ParseCSV() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ParseCSV() returned %d targets, want 1: %+v", len(got), got)
	}
	if got[0].Email != "jane.roe@example.invalid" {
		t.Fatalf("target = %+v, want the record that follows the skipped one", got[0].BaseRecipient)
	}
}

// TestParseCSVFieldLimitCoversCarriedValues checks the limit still applies when
// a record inherits an oversized value from an earlier, skipped record because
// it does not carry that column itself.
func TestParseCSVFieldLimitCoversCarriedValues(t *testing.T) {
	content := fmt.Sprintf(
		"Email,Last Name,First Name\nnot-an-address,Doe,%s\njane.roe@example.invalid,Roe\n",
		strings.Repeat("a", MaxImportFieldCharacters+1),
	)
	if _, err := parseImportRequest(t, map[string]string{"targets.csv": content}); !errors.Is(err, ErrCSVFieldTooLong) {
		t.Fatalf("ParseCSV() error = %v, want %v", err, ErrCSVFieldTooLong)
	}
}

func TestParseCSVNeverTruncates(t *testing.T) {
	content := fmt.Sprintf(
		"First Name,Last Name,Email\n%s,Doe,john.doe@example.invalid\n",
		strings.Repeat("a", MaxImportFieldCharacters*10),
	)
	got, err := parseImportRequest(t, map[string]string{"targets.csv": content})
	if !errors.Is(err, ErrCSVFieldTooLong) {
		t.Fatalf("ParseCSV() error = %v, want %v", err, ErrCSVFieldTooLong)
	}
	for _, target := range got {
		if len([]rune(target.FirstName)) > MaxImportFieldCharacters {
			t.Fatalf("target kept an oversized value: %d characters", len([]rune(target.FirstName)))
		}
	}
}

func TestParseCSVColumnLimitBoundaries(t *testing.T) {
	buildColumns := func(count int) string {
		header := new(strings.Builder)
		row := new(strings.Builder)
		header.WriteString("First Name,Last Name,Email")
		row.WriteString("John,Doe,john.doe@example.invalid")
		for i := 3; i < count; i++ {
			fmt.Fprintf(header, ",column%d", i)
			fmt.Fprintf(row, ",value%d", i)
		}
		return header.String() + "\n" + row.String() + "\n"
	}

	got, err := parseImportRequest(t, map[string]string{"targets.csv": buildColumns(MaxImportColumns)})
	if err != nil {
		t.Fatalf("ParseCSV() at the column limit error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ParseCSV() returned %d targets, want 1", len(got))
	}

	if _, err := parseImportRequest(t, map[string]string{"targets.csv": buildColumns(MaxImportColumns + 1)}); !errors.Is(err, ErrCSVTooManyColumns) {
		t.Fatalf("ParseCSV() error = %v, want %v", err, ErrCSVTooManyColumns)
	}
}

func TestParseCSVRejectsMalformedContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "unterminated quote", content: "First Name,Last Name,Email\n\"John,Doe,john.doe@example.invalid\n"},
		{name: "bare quote inside field", content: "First Name,Last Name,Email\nJo\"hn\",Doe,john.doe@example.invalid\n"},
		{name: "malformed header", content: "\"First Name,Last Name,Email\nJohn,Doe,john.doe@example.invalid\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseImportRequest(t, map[string]string{"targets.csv": test.content})
			if !errors.Is(err, ErrCSVMalformed) {
				t.Fatalf("ParseCSV() error = %v, want %v", err, ErrCSVMalformed)
			}
			for _, target := range got {
				if target.Email == "" {
					t.Fatal("malformed content produced a target without an address")
				}
			}
		})
	}
}

// TestParseCSVRejectsTruncatedBody covers the upload that used to spin forever:
// a body cut mid-part must terminate with an error instead of looping.
func TestParseCSVRejectsTruncatedBody(t *testing.T) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("files[]", "targets.csv")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("First Name,Last Name,Email\nJohn,Doe,john.doe@example.invalid\n")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	truncated := body.Bytes()[:body.Len()-16]

	request, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/api/import/group", bytes.NewReader(truncated))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	done := make(chan error, 1)
	go func() {
		_, parseErr := ParseCSV(request)
		done <- parseErr
	}()

	select {
	case parseErr := <-done:
		if parseErr == nil {
			t.Fatal("ParseCSV() accepted a truncated body")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("ParseCSV() did not terminate on a truncated body")
	}
}

// TestParseCSVRejectsCorruptBoundary covers the body that used to dereference a
// nil part after the multipart reader failed.
func TestParseCSVRejectsCorruptBoundary(t *testing.T) {
	request, err := http.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/api/import/group",
		strings.NewReader("--boundary\r\nContent-Disposition: form-data; name=\"files[]\"; filename=\"a.csv\"\r\n\r\nnever terminated"),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")

	if _, err := ParseCSV(request); err == nil {
		t.Fatal("ParseCSV() accepted a corrupt multipart body")
	}
}

func TestParseCSVRejectsNonMultipartBody(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/api/import/group", strings.NewReader(`{"targets": []}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	if _, err := ParseCSV(request); err == nil {
		t.Fatal("ParseCSV() accepted a body that is not multipart")
	}
}

// TestParseCSVStopsReadingBeyondRequestLimit proves the request cap is enforced
// while the body streams, so an oversized upload is never buffered whole.
func TestParseCSVStopsReadingBeyondRequestLimit(t *testing.T) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("files[]", "targets.csv")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(buildRecords(2000))); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	counter := &countingReader{reader: bytes.NewReader(body.Bytes())}
	const limit = 4096
	request, err := http.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/api/import/group",
		io.LimitReader(counter, limit),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	if _, err := ParseCSV(request); err == nil {
		t.Fatal("ParseCSV() accepted a body cut at the request limit")
	}
	if counter.read > limit {
		t.Fatalf("ParseCSV() read %d bytes, want at most %d", counter.read, limit)
	}
}

type countingReader struct {
	reader io.Reader
	read   int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.read += n
	return n, err
}

// TestImportFieldLimitMatchesTargetSchema keeps the field limit aligned with the
// shipped target columns so an accepted import always fits the database.
func TestImportFieldLimitMatchesTargetSchema(t *testing.T) {
	schemas := []string{
		filepath.Join("..", "db", "db_sqlite3", "migrations", "20160118194630_init.sql"),
		filepath.Join("..", "db", "db_mysql", "migrations", "20160118194630_init.sql"),
	}
	tablePattern := regexp.MustCompile("CREATE TABLE IF NOT EXISTS [`\"]?targets[`\"]?")
	columnPattern := regexp.MustCompile(`"?(first_name|last_name|email|position)"? varchar\((\d+)\)`)

	for _, schema := range schemas {
		content, err := os.ReadFile(schema)
		if err != nil {
			t.Fatalf("read %s: %v", schema, err)
		}
		definition := ""
		for _, line := range strings.Split(string(content), "\n") {
			if tablePattern.MatchString(line) {
				definition = line
				break
			}
		}
		if definition == "" {
			t.Fatalf("%s does not declare the targets table", schema)
		}
		matches := columnPattern.FindAllStringSubmatch(definition, -1)
		if len(matches) != 4 {
			t.Fatalf("%s declared %d target columns, want 4", schema, len(matches))
		}
		for _, match := range matches {
			size, err := strconv.Atoi(match[2])
			if err != nil {
				t.Fatalf("parse column size in %s: %v", schema, err)
			}
			if size != MaxImportFieldCharacters {
				t.Fatalf(
					"%s declares %s as varchar(%d) while the import limit is %d",
					schema, match[1], size, MaxImportFieldCharacters,
				)
			}
		}
	}
}

// FuzzParseCSVTargets exercises the parsing and column mapping in isolation: no
// database, socket, or file system is involved. The seed corpus doubles as a
// fast regression smoke test during a normal `go test` run.
func FuzzParseCSVTargets(f *testing.F) {
	f.Add("First Name,Last Name,Email\nJohn,Doe,john.doe@example.invalid\n")
	f.Add("\xef\xbb\xbfFirst Name,Last Name,Email,Position\r\nZoé,Doe,zoe@example.invalid,Analyst\r\n")
	f.Add("First Name,Last Name,Email\n\"John,Doe,john.doe@example.invalid\n")
	f.Add("First Name,Last Name,Email\nJohn,Doe,not-an-address\n")
	f.Add("column a,column b\nvalue a,value b\n")
	f.Add("First Name,Last Name,Email\n")
	f.Add("")

	f.Fuzz(func(t *testing.T, content string) {
		got, err := parseCSVTargets(strings.NewReader(content), MaxImportRecords)
		if err != nil && !isExpectedImportError(err) {
			t.Fatalf("parseCSVTargets() unexpected error = %v", err)
		}
		if len(got) > MaxImportRecords {
			t.Fatalf("parseCSVTargets() returned %d targets, want at most %d", len(got), MaxImportRecords)
		}
		for _, target := range got {
			for _, value := range []string{target.FirstName, target.LastName, target.Email, target.Position} {
				if len([]rune(value)) > MaxImportFieldCharacters {
					t.Fatalf("parseCSVTargets() returned a %d character field", len([]rune(value)))
				}
			}
		}
	})
}

func isExpectedImportError(err error) bool {
	for _, expected := range []error{
		ErrCSVMalformed,
		ErrCSVTooManyColumns,
		ErrCSVTooManyRecords,
		ErrCSVFieldTooLong,
		errCSVEmptyPart,
	} {
		if errors.Is(err, expected) {
			return true
		}
	}
	return false
}
