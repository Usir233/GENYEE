package docs_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestW7IMG01_ReadmeArtworkScriptsImportCleanlyAndMatchVendorCount(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	srcDir, err := filepath.Abs(filepath.Join("images", "readme", "src"))
	if err != nil {
		t.Fatal(err)
	}
	genPath := filepath.Join(srcDir, "generate.py")
	spotPath := filepath.Join(srcDir, "spotlights.py")

	script := `
import ast, pathlib, sys

gen_src = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
spot_src = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")

gen_tree = ast.parse(gen_src, filename="generate.py")
spot_tree = ast.parse(spot_src, filename="spotlights.py")

for node in ast.walk(gen_tree):
    if isinstance(node, ast.Import):
        for alias in node.names:
            assert alias.name != "numpy", "generate.py must not import unused numpy"
    if isinstance(node, ast.ImportFrom):
        assert node.module != "numpy", "generate.py must not import unused numpy"

def has_main_guard(tree):
    for node in tree.body:
        if isinstance(node, ast.If):
            txt = ast.unparse(node.test)
            if "__name__" in txt and "__main__" in txt:
                return True
    return False

assert has_main_guard(gen_tree), "generate.py must guard file generation under if __name__ == '__main__':"
assert has_main_guard(spot_tree), "spotlights.py must guard file generation under if __name__ == '__main__':"
assert "27 家厂商" not in gen_src and "27 vendors" not in gen_src and "27 ベンダー" not in gen_src and "27개 벤더" not in gen_src, (
    "generate.py still hardcodes stale 27 built-in model vendors instead of 30"
)
assert "30 家厂商" in gen_src and "30 vendors" in gen_src, "generate.py must list 30 built-in model vendors"
assert "ImageFont.load_default" in gen_src or "DejaVuSans" in gen_src, (
    "generate.py must provide non-macOS font fallback when Arial.ttf is absent"
)
print("README_SRC_OK")
`
	cmd := exec.Command(py, "-c", script, genPath, spotPath)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("readme artwork source check failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "README_SRC_OK") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}
