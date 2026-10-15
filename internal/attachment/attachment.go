// Package attachment lists Jira and Confluence attachments and downloads
// them safely: implements `atl attachment`.
package attachment

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RyabovNick/atl/internal/atlassian"
	"github.com/RyabovNick/atl/internal/cli"
)

const defaultMaxBytes = 50 << 20

var (
	exactKeyRE  = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)
	jiraAttRE   = regexp.MustCompile(`^\d+$`)
	confAttIDRE = regexp.MustCompile(`^att\d+$`)
)

// Attachment is a Jira attachment, or a Confluence one converted to the same shape.
type Attachment struct {
	ID       string           `json:"id"`
	Filename string           `json:"filename"`
	Size     int64            `json:"size"`
	MimeType string           `json:"mimeType"`
	Author   *atlassian.Named `json:"author"`
	Created  string           `json:"created"`
	Content  string           `json:"content"`
}

type confAttachment struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Extensions struct {
		MediaType string `json:"mediaType"`
		FileSize  int64  `json:"fileSize"`
	} `json:"extensions"`
	Version struct {
		When string           `json:"when"`
		By   *atlassian.Named `json:"by"`
	} `json:"version"`
	Links struct {
		Download string `json:"download"`
	} `json:"_links"`
}

func (a confAttachment) asAttachment() Attachment {
	return Attachment{
		ID: a.ID, Filename: a.Title, Size: a.Extensions.FileSize, MimeType: a.Extensions.MediaType,
		Author: a.Version.By, Created: a.Version.When, Content: a.Links.Download,
	}
}

func humanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v, unit := float64(n)/1024, "KB"
	for _, u := range []string{"MB", "GB"} {
		if v < 1024 {
			break
		}
		v, unit = v/1024, u
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}

func attachmentLine(a Attachment) string {
	parts := []string{humanSize(a.Size)}
	for _, p := range []string{a.MimeType, atlassian.NameOf(a.Author)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if a.Created != "" {
		parts = append(parts, atlassian.ShortDate(a.Created))
	}
	return fmt.Sprintf("%s — %s · id %s", a.Filename, strings.Join(parts, " · "), a.ID)
}

// safeFilename keeps a server-supplied name inside the target directory and visible in it.
func safeFilename(name, fallback string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return '_'
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(name), "."))
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		stem := name[:200-len(ext)]
		for !utf8.ValidString(stem) {
			stem = stem[:len(stem)-1]
		}
		name = stem + ext
	}
	if name == "" {
		return fallback
	}
	return name
}

// createUnique opens a new file in dir, adding -1, -2… before the extension rather than overwriting.
func createUnique(dir, name string) (*os.File, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := range 1000 {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		// name comes from safeFilename, which strips path separators and leading dots.
		f, err := os.OpenFile(filepath.Join(dir, candidate), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return f, err
	}
	return nil, fmt.Errorf("too many files named %s in %s", name, dir)
}

type download struct {
	att    Attachment
	source *atlassian.Client
}

type savedFile struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Filename string `json:"filename"`
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	MimeType string `json:"mimeType"`
}

func save(d download, dir string, maxBytes int64) (savedFile, error) {
	a := d.att
	if a.Size > maxBytes {
		return savedFile{}, cli.Fail(cli.ExitAPI, "%s is %s, over the %s limit; raise --max-bytes", a.Filename, humanSize(a.Size), humanSize(maxBytes))
	}
	if a.Content == "" {
		return savedFile{}, cli.Fail(cli.ExitAPI, "%s has no download link", a.Filename)
	}
	f, err := createUnique(dir, safeFilename(a.Filename, "attachment-"+a.ID))
	if err != nil {
		return savedFile{}, cli.Fail(cli.ExitAPI, "%v", err)
	}
	n, err := d.source.Download(a.Content, f, maxBytes)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cli.Fail(cli.ExitAPI, "writing %s: %v", f.Name(), cerr)
	}
	if err != nil {
		os.Remove(f.Name())
		return savedFile{}, err
	}
	path, _ := filepath.Abs(f.Name())
	return savedFile{ID: a.ID, Source: d.source.Name, Filename: a.Filename, Path: path, Bytes: n, MimeType: a.MimeType}, nil
}

// ForIssue lists a Jira issue's attachments.
func ForIssue(jira *atlassian.Client, key string) ([]Attachment, error) {
	var is struct {
		Fields struct {
			Attachment []Attachment `json:"attachment"`
		} `json:"fields"`
	}
	_, err := jira.Get("/rest/api/2/issue/"+url.PathEscape(key), url.Values{"fields": {"attachment"}}, &is)
	return is.Fields.Attachment, err
}

// ForPage lists a Confluence page's attachments.
func ForPage(conf *atlassian.Client, pageID string) ([]Attachment, error) {
	var res struct {
		Results []confAttachment `json:"results"`
	}
	params := url.Values{"limit": {"100"}, "expand": {"version"}}
	if _, err := conf.Get("/rest/api/content/"+url.PathEscape(pageID)+"/child/attachment", params, &res); err != nil {
		return nil, err
	}
	out := make([]Attachment, len(res.Results))
	for i, r := range res.Results {
		out[i] = r.asAttachment()
	}
	return out, nil
}

// resolveDownloads turns arguments into attachments: one issue key (or the
// branch's) means all of its attachments; otherwise each argument is a Jira
// attachment id or a Confluence one (att<digits>).
func resolveDownloads(pos []string, stderr io.Writer) ([]download, string, error) {
	clients := map[string]*atlassian.Client{}
	clientFor := func(name string) (*atlassian.Client, error) {
		if c, ok := clients[name]; ok {
			return c, nil
		}
		c, err := atlassian.NewClient(name)
		clients[name] = c
		return c, err
	}

	if len(pos) == 0 || (len(pos) == 1 && exactKeyRE.MatchString(strings.ToUpper(pos[0]))) {
		key, err := cli.KeyArg(pos, "attachment", stderr)
		if err != nil {
			return nil, "", err
		}
		jira, err := clientFor("jira")
		if err != nil {
			return nil, "", err
		}
		atts, err := ForIssue(jira, key)
		if err != nil {
			return nil, "", err
		}
		ds := make([]download, len(atts))
		for i, a := range atts {
			ds[i] = download{a, jira}
		}
		return ds, key, nil
	}

	var ds []download
	for _, id := range pos {
		switch {
		case jiraAttRE.MatchString(id):
			jira, err := clientFor("jira")
			if err != nil {
				return nil, "", err
			}
			var a Attachment
			if _, err := jira.Get("/rest/api/2/attachment/"+id, nil, &a); err != nil {
				return nil, "", err
			}
			ds = append(ds, download{a, jira})
		case confAttIDRE.MatchString(id):
			conf, err := clientFor("confluence")
			if err != nil {
				return nil, "", err
			}
			var a confAttachment
			if _, err := conf.Get("/rest/api/content/"+id, url.Values{"expand": {"version"}}, &a); err != nil {
				return nil, "", err
			}
			ds = append(ds, download{a.asAttachment(), conf})
		default:
			return nil, "", cli.Fail(cli.ExitUsage, "%q is neither an issue key, a Jira attachment id nor a Confluence one (att<digits>)", id)
		}
	}
	return ds, pos[0], nil
}

// Run implements `atl attachment`.
func Run(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("attachment", "[KEY | ID...]",
		"Download attachments and print where they were saved.\n"+
			"KEY downloads all of an issue's attachments and defaults to the current branch's issue;\n"+
			"IDs are Jira attachment ids (from atl issue) or Confluence ones, att<digits> (from atl page).", stderr)
	dir := fs.String("dir", "", "save into this directory (default: a new directory under the system temp dir)")
	maxBytes := fs.Int64("max-bytes", defaultMaxBytes, "refuse any file larger than this")
	asJSON := fs.Bool("json", false, "print the saved files as JSON")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	if *maxBytes <= 0 {
		return cli.Fail(cli.ExitUsage, "--max-bytes must be positive")
	}
	downloads, label, err := resolveDownloads(pos, stderr)
	if err != nil {
		return err
	}
	if len(downloads) == 0 {
		fmt.Fprintf(stdout, "%s has no attachments\n", label)
		return nil
	}

	target := *dir
	if target == "" {
		if target, err = os.MkdirTemp("", "atl-attachments-"+safeFilename(label, "files")+"-"); err != nil {
			return cli.Fail(cli.ExitAPI, "%v", err)
		}
	} else if err := os.MkdirAll(target, 0o700); err != nil {
		return cli.Fail(cli.ExitUsage, "--dir: %v", err)
	}

	var saved []savedFile
	var failed []string
	for _, d := range downloads {
		s, err := save(d, target, *maxBytes)
		if err != nil {
			fmt.Fprintf(stderr, "atl: %s (id %s): %v\n", d.att.Filename, d.att.ID, err)
			failed = append(failed, d.att.ID)
			continue
		}
		saved = append(saved, s)
	}

	if *asJSON {
		if saved == nil {
			saved = []savedFile{}
		}
		raw, err := json.Marshal(saved)
		if err != nil {
			return cli.Fail(cli.ExitAPI, "encoding JSON: %v", err)
		}
		if err := cli.WriteJSON(stdout, raw); err != nil {
			return err
		}
	} else if len(saved) > 0 {
		abs, _ := filepath.Abs(target)
		fmt.Fprintf(stdout, "Saved %d of %d to %s:\n", len(saved), len(downloads), abs)
		for _, s := range saved {
			fmt.Fprintf(stdout, "- %s (%s, %s)\n", s.Path, humanSize(s.Bytes), cli.Or(s.MimeType, "unknown type"))
		}
	}
	if len(failed) > 0 {
		return cli.Fail(cli.ExitAPI, "%d of %d downloads failed: %s", len(failed), len(downloads), strings.Join(failed, ", "))
	}
	return nil
}

// Write adds an Attachments section listing atts, with hint saying how to download them.
func Write(b *strings.Builder, atts []Attachment, hint string) {
	if len(atts) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Attachments (%d)\n\n", len(atts))
	for _, a := range atts {
		fmt.Fprintf(b, "- %s\n", attachmentLine(a))
	}
	fmt.Fprintf(b, "\n(download: %s)\n", hint)
}
