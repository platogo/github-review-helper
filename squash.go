package main

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/google/go-github/github"
	"github.com/salemove/github-review-helper/git"
)

var ErrRebase = errors.New("Rebase failed")

func isSquashCommand(comment string) bool {
	return strings.TrimSpace(comment) == "!squash"
}

func handleSquashCommand(issueComment IssueComment, gitRepos git.Repos, pullRequests PullRequests, repositories Repositories) Response {
	pr, errResp := getPR(issueComment, pullRequests)
	if errResp != nil {
		return errResp
	}
	return squashAndReportFailure(pr, gitRepos, repositories)
}

func checkForFixupCommits(pullRequestEvent PullRequestEvent, pullRequests PullRequests, repositories Repositories) Response {
	log.Printf("Checking for fixup commits for PR %s.\n", pullRequestEvent.Issue().FullName())
	commits, errResp := getCommits(pullRequestEvent, pullRequests)
	if errResp != nil {
		return errResp
	}
	if !includesFixupCommits(commits) {
		status := createSquashStatus("success", "No fixup! or squash! commits to be squashed")
		if errResp := setStatusForPREvent(pullRequestEvent, status, repositories); errResp != nil {
			return errResp
		}
		return SuccessResponse{}
	}
	status := createSquashStatus("pending", "This PR needs to be squashed with !squash before merging")
	if errResp := setStatusForPREvent(pullRequestEvent, status, repositories); errResp != nil {
		return errResp
	}
	return SuccessResponse{}
}

func includesFixupCommits(commits []*github.RepositoryCommit) bool {
	for _, commit := range commits {
		if strings.HasPrefix(*commit.Commit.Message, "fixup! ") || strings.HasPrefix(*commit.Commit.Message, "squash! ") {
			return true
		}
	}
	return false
}

func createSquashStatus(state, description string) *github.RepoStatus {
	return &github.RepoStatus{
		State:       github.String(state),
		Description: github.String(description),
		Context:     github.String(githubStatusSquashContext),
	}
}

func squashAndReportFailure(pr *github.PullRequest, gitRepos git.Repos, repositories Repositories) Response {
	log.Printf("Squashing %s that's going to be merged into %s\n", *pr.Head.Ref, *pr.Base.Ref)
	err := squash(pr, gitRepos, repositories)
	if err == ErrRebase {
		log.Printf("Failed to autosquash the commits with an interactive rebase: %s. Setting a failure status.\n", err)
		status := createSquashStatus("failure", "Automatic squash failed. Please squash manually")
		if errResp := setStatusForPR(pr, status, repositories); errResp != nil {
			return errResp
		}
		return SuccessResponse{}
	} else if err != nil {
		return ErrorResponse{err, http.StatusInternalServerError, "Failed to squash the commits in the PR"}
	}
	return SuccessResponse{}
}

func squash(pr *github.PullRequest, gitRepos git.Repos, repositories Repositories) error {
	headRepository := headRepository(pr)
	gitRepo, err := gitRepos.GetUpdatedRepo(headRepository.URL, headRepository.Owner, headRepository.Name)
	if err != nil {
		log.Println(err)
		return errors.New("Failed to update the local repo")
	}
	if err = gitRepo.RebaseAutosquash("origin/"+*pr.Base.Ref, *pr.Head.SHA); err != nil {
		log.Println(err)
		return ErrRebase
	}
	if err = gitRepo.ForcePushHeadTo(*pr.Head.Ref); err != nil {
		log.Println(err)
		return errors.New("Failed to push the squashed version")
	}
	return nil
}
