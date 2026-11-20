package seccheck

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func pom(deps ...string) string {
	var b strings.Builder
	b.WriteString("<project><modelVersion>4.0.0</modelVersion><groupId>acme</groupId><artifactId>app</artifactId><version>1</version><dependencies>")
	for _, d := range deps {
		g, rest, _ := strings.Cut(d, ":")
		a, v, _ := strings.Cut(rest, "@")
		b.WriteString("<dependency><groupId>" + g + "</groupId><artifactId>" + a + "</artifactId><version>" + v + "</version></dependency>")
	}
	b.WriteString("</dependencies></project>\n")
	return b.String()
}

func osvStub(t *testing.T) *httptest.Server {
	vulns := map[string]string{
		"org.apache.logging.log4j:log4j-core": `{"id":"GHSA-jfh8","aliases":["CVE-2021-44228"],"summary":"Log4Shell","database_specific":{"severity":"CRITICAL"},
			"affected":[{"package":{"name":"org.apache.logging.log4j:log4j-core","ecosystem":"Maven"},"ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"2.0"},{"fixed":"2.15.0"}]}]}]}`,
		"com.google.guava:guava": `{"id":"GHSA-7g45","summary":"Temp directory exposure","database_specific":{"severity":"MODERATE"},
			"affected":[{"package":{"name":"com.google.guava:guava","ecosystem":"Maven"},"ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"0"},{"fixed":"32.0.0-android"}]}]}]}`,
	}
	ids := map[string]string{"GHSA-jfh8": vulns["org.apache.logging.log4j:log4j-core"], "GHSA-7g45": vulns["com.google.guava:guava"]}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/querybatch":
			var req struct {
				Queries []struct {
					Package struct{ Name, Ecosystem string } `json:"package"`
				} `json:"queries"`
			}
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &req)
			var res []map[string]any
			for _, q := range req.Queries {
				var vs []map[string]string
				if v, ok := vulns[q.Package.Name]; ok {
					var rec struct{ ID string }
					_ = json.Unmarshal([]byte(v), &rec)
					vs = append(vs, map[string]string{"id": rec.ID})
				}
				res = append(res, map[string]any{"vulns": vs})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": res})
		case strings.HasPrefix(r.URL.Path, "/vulns/"):
			v, ok := ids[strings.TrimPrefix(r.URL.Path, "/vulns/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(v))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestJavaDependenciesNewAndLegacy(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "pom.xml", pom("com.google.guava:guava@31.0-jre"))
	base := commitAll(t, dir, "base")
	write(t, dir, "pom.xml", pom("com.google.guava:guava@31.0-jre", "org.apache.logging.log4j:log4j-core@2.14.1"))
	commitAll(t, dir, "log4j")
	srv := osvStub(t)
	f := &fake{out: map[string]string{"gitleaks": "[]"}, err: map[string]error{}}
	rep, err := Run(context.Background(), dir, Options{RangeStart: base, IncludeLegacy: true, Run: f.run, LookPath: everywhere, OSV: srv.URL, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"new  block  osv maven  critical  pom.xml org.apache.logging.log4j:log4j-core@2.14.1 -> 2.15.0  CVE-2021-44228  Log4Shell",
		"legacy  warn  osv maven  medium  pom.xml com.google.guava:guava@31.0-jre -> 32.0.0-android  GHSA-7g45  Temp directory exposure",
	}, "\n")
	if got := lines(rep); got != want || !rep.Blocking() {
		t.Errorf("findings:\n%s\nwant:\n%s", got, want)
	}
}

func TestJavaDependenciesSkipWhenNoManifestChanged(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "pom.xml", pom("org.apache.logging.log4j:log4j-core@2.14.1"))
	base := commitAll(t, dir, "base")
	write(t, dir, "App.java", "class App {}\n")
	commitAll(t, dir, "code")
	f := &fake{out: map[string]string{"gitleaks": "[]"}, err: map[string]error{}}
	rep, err := Run(context.Background(), dir, Options{RangeStart: base, Run: f.run, LookPath: everywhere, OSV: "http://127.0.0.1:1"})
	if err != nil || len(rep.Findings) != 0 || len(rep.Skips) != 0 {
		t.Errorf("a range without a manifest change queried OSV: %+v %v", rep, err)
	}
	rep, _ = Run(context.Background(), dir, Options{RangeStart: base, IncludeLegacy: true, Run: f.run, LookPath: everywhere, OSV: "http://127.0.0.1:1"})
	if len(rep.Skips) != 1 || rep.Skips[0].Tool != "osv maven" {
		t.Errorf("an unreachable OSV isn't a skip: %+v", rep.Skips)
	}
}

func TestSpotBugsSuppressionIsReported(t *testing.T) {
	if m := suppressionIn("src/App.java", `    @SuppressFBWarnings(value = "SQL_INJECTION_JDBC", justification = "constant table name")`); m == nil || m[1] != "SuppressFBWarnings" {
		t.Errorf("match %v", m)
	}
	if m := suppressionIn("README.md", "@SuppressFBWarnings"); m != nil {
		t.Errorf("prose matched: %v", m)
	}
}
