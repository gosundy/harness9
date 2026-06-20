package main

import (
	"strings"
	"testing"
)

// TestDefaultBootstrapCmd 验证默认自举命令包含恢复 pip、editable 安装当前仓库、安装 pytest
// 三要素——这是"可配置·默认自举"路线让真实测试可运行的基础（接通 sandbox.BootstrapCmd 接缝）。
func TestDefaultBootstrapCmd(t *testing.T) {
	cmd := defaultBootstrapCmd(Instance{InstanceID: "pallets__flask-4992", Repo: "pallets/flask"})
	for _, want := range []string{"ensurepip", "pip install -e .", "pytest"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("bootstrap 命令应包含 %q，got: %q", want, cmd)
		}
	}
}

// TestLooksLikeTestRun 验证测试运行识别启发式：真实测试调用判 true，只读/安装命令判 false。
func TestLooksLikeTestRun(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"python -m pytest tests/test_config.py -q", true},
		{"pytest tests/test_cli.py::TestRoutes::test_host", true},
		{"py.test -k redirect", true},
		{"python -m unittest discover", true},
		{"./tests/runtests.py admin_views", true},
		{"python setup.py test", true},
		{"tox -e py311", true},
		{"nosetests xarray/tests", true},
		// 只读 / 安装 / 探索命令不应误判为测试运行
		{"grep -rn pytest tests/", false},
		{"ls -la", false},
		{"python -m pip install -e . pytest", false},
		{"cat tests/test_config.py", false},
		{"", false},
	}
	for _, c := range cases {
		if got := looksLikeTestRun(c.cmd); got != c.want {
			t.Errorf("looksLikeTestRun(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}
