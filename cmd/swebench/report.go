package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"
)

// loadExistingIDs 读取已有的 predictions.jsonl，返回已处理的 instance_id 集合。
// 文件不存在时返回空 map（不报错），支持首次运行。
func loadExistingIDs(path string) (map[string]bool, error) {
	ids := make(map[string]bool)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return ids, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var p Prediction
		if err := json.Unmarshal([]byte(scanner.Text()), &p); err == nil && p.InstanceID != "" {
			ids[p.InstanceID] = true
		}
	}
	return ids, scanner.Err()
}

// appendPrediction 将单条 Prediction 追加写入 predictions.jsonl（立即 flush）。
func appendPrediction(path string, p Prediction) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("打开 predictions 文件失败: %w", err)
	}
	defer f.Close()
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

const summaryTmpl = `# SWE-bench Lite Run Summary

- 开始时间: {{.StartTime}}
- 结束时间: {{.EndTime}}
- 总实例数: {{.Total}}
- 成功生成 patch: {{.WithPatch}} / {{.Total}}
- 空 patch（agent 无改动）: {{.EmptyPatch}}
- 运行出错: {{.Errors}}

## 按 Repo 分布

| Repo | 实例数 | 有 patch | 空 patch | 出错 |
|------|--------|---------|---------|------|
{{- range .Repos}}
| {{.Name}} | {{.Total}} | {{.WithPatch}} | {{.EmptyPatch}} | {{.Errors}} |
{{- end}}

## 评估命令

` + "```" + `bash
pip install swebench
python -m swebench.harness.run_evaluation \
    --dataset_name princeton-nlp/SWE-bench_Lite \
    --predictions_path ./swebench-results/predictions.jsonl \
    --max_workers 4 \
    --run_id harness9-lite-v1
` + "```" + `
`

type repoStats struct {
	Name       string
	Total      int
	WithPatch  int
	EmptyPatch int
	Errors     int
}

type summaryData struct {
	StartTime  string
	EndTime    string
	Total      int
	WithPatch  int
	EmptyPatch int
	Errors     int
	Repos      []repoStats
}

// writeSummary 将运行摘要写入 outputDir/run_summary.md。
func writeSummary(outputDir string, results []RunResult, start, end time.Time) error {
	byRepo := make(map[string]*repoStats)
	sd := summaryData{
		StartTime: start.Format("2006-01-02 15:04:05"),
		EndTime:   end.Format("2006-01-02 15:04:05"),
		Total:     len(results),
	}
	for _, r := range results {
		if byRepo[r.Instance.Repo] == nil {
			byRepo[r.Instance.Repo] = &repoStats{Name: r.Instance.Repo}
		}
		rs := byRepo[r.Instance.Repo]
		rs.Total++
		switch {
		case r.Error != nil:
			sd.Errors++
			rs.Errors++
		case r.Patch == "":
			sd.EmptyPatch++
			rs.EmptyPatch++
		default:
			sd.WithPatch++
			rs.WithPatch++
		}
	}
	for _, rs := range byRepo {
		sd.Repos = append(sd.Repos, *rs)
	}
	sort.Slice(sd.Repos, func(i, j int) bool { return sd.Repos[i].Name < sd.Repos[j].Name })

	tmpl, err := template.New("summary").Parse(summaryTmpl)
	if err != nil {
		return err
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, sd); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outputDir, "run_summary.md"), []byte(sb.String()), 0644)
}
