package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/go-github/v32/github"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/oauth2"
)

// SearchPerPage is specified for pagination
const SearchPerPage = 100

// CollectIntervalSeconds specifies the interval for collecting data from GitHub
const CollectIntervalSeconds = 300

// LanguageLabels contain label names should be detected as languages
var LanguageLabels = map[string]bool{
	"ruby":       true,
	"javascript": true,
	"python":     true,
	"elixir":     true,
	"rust":       true,
	"java":       true,
	"go":         true,
	"elm":        true,
}

const namespace = "github_postmortem"

var (
	githubUsername string
	githubReponame string

	client *github.Client
	ctx    = context.Background()

	openPullRequestsGauge *prometheus.GaugeVec
)

func init() {
	src := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	httpClient := oauth2.NewClient(context.Background(), src)
	client = github.NewClient(httpClient)

}

func searchIssues() chan *github.Issue {
	issueChan := make(chan *github.Issue)

	go func() {
		var lastCreated *time.Time
		for {
			query := fmt.Sprintf("repo:%s/%s is:open label:Postmortem label:SRE", githubUsername, githubReponame)
			if lastCreated != nil {
				query = query + " created:>" + lastCreated.Format(time.RFC3339)
			}

			opts := &github.SearchOptions{
				Sort:  "created",
				Order: "asc",
				ListOptions: github.ListOptions{
					Page:    1,
					PerPage: SearchPerPage,
				},
			}
			result, _, err := client.Search.Issues(ctx, query, opts)
			if err != nil {
				log.Fatalf("Failed to fetch search result: %s", err)
			}

			for _, issue := range result.Issues {
				issueChan <- issue
				lastCreated = issue.CreatedAt
			}

			if len(result.Issues) < SearchPerPage {
				break
			}
		}

		close(issueChan)
	}()

	return issueChan
}

type postmortemPullRequest struct {
	Library     string
	Language    string
	FromVersion string
	ToVersion   string
	Directory   string
	Security    bool
}

func main() {
	githubUsername = os.Getenv("GITHUB_USERNAME")
	if githubUsername == "" {
		log.Fatal("GITHUB_USERNAME is not set")
	}

	githubReponame = os.Getenv("GITHUB_REPONAME")
	if githubReponame == "" {
		log.Fatal("GITHUB_REPONAME is not set")
	}

	openPullRequestsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "open_issues",
			Help:      "Open Postmortem issue",
			ConstLabels: prometheus.Labels{
				"username":      githubUsername,
				"reponame":      githubReponame,
				"full_reponame": fmt.Sprintf("%s/%s", githubUsername, githubReponame),
			},
		},
		[]string{"title"},
	)

	prometheus.MustRegister(
		openPullRequestsGauge,
	)

	go collectTicker()

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe("0.0.0.0:8000", nil))
}

func collect() {
	openPullRequestsGauge.Reset()

	for issue := range searchIssues() {
		labels := prometheus.Labels{
			"title": issue.GetTitle(),
		}
		openPullRequestsGauge.With(labels).Set(1)
	}
}

func collectTicker() {
	for {
		collect()
		time.Sleep(CollectIntervalSeconds * time.Second)
	}
}
