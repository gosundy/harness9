package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDataset(t *testing.T) {
	content := `{"instance_id":"django__django-1","repo":"django/django","base_commit":"abc123","problem_statement":"Fix bug A","hints_text":""}
{"instance_id":"astropy__astropy-2","repo":"astropy/astropy","base_commit":"def456","problem_statement":"Fix bug B","hints_text":"hint"}
`
	tmp := filepath.Join(t.TempDir(), "lite.jsonl")
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	instances, err := loadDataset(tmp)
	if err != nil {
		t.Fatalf("loadDataset error: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("want 2 instances, got %d", len(instances))
	}
	if instances[0].InstanceID != "django__django-1" {
		t.Errorf("want django__django-1, got %s", instances[0].InstanceID)
	}
	if instances[1].ProblemStatement != "Fix bug B" {
		t.Errorf("want 'Fix bug B', got %s", instances[1].ProblemStatement)
	}
}

func TestLoadDatasetFileNotFound(t *testing.T) {
	_, err := loadDataset("/nonexistent/path.jsonl")
	if err == nil {
		t.Fatal("want error for missing file, got nil")
	}
}

func TestSampleByRepo(t *testing.T) {
	instances := []Instance{
		{InstanceID: "django-1", Repo: "django/django"},
		{InstanceID: "django-2", Repo: "django/django"},
		{InstanceID: "django-3", Repo: "django/django"},
		{InstanceID: "astropy-1", Repo: "astropy/astropy"},
		{InstanceID: "astropy-2", Repo: "astropy/astropy"},
		{InstanceID: "flask-1", Repo: "pallets/flask"},
	}

	sampled := sampleByRepo(instances, 2, 42)

	// django 3条取2, astropy 2条取2, flask 1条取1 → 共 5 条
	if len(sampled) != 5 {
		t.Fatalf("want 5 sampled instances, got %d", len(sampled))
	}

	count := make(map[string]int)
	for _, inst := range sampled {
		count[inst.Repo]++
	}
	if count["django/django"] > 2 {
		t.Errorf("django sample exceeds limit: %d", count["django/django"])
	}
}
