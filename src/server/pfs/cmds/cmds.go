package cmds

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"golang.org/x/sync/errgroup"

	"github.com/pachyderm/pachyderm/src/client"
	pfsclient "github.com/pachyderm/pachyderm/src/client/pfs"
	"github.com/pachyderm/pachyderm/src/server/pfs/fuse"
	"github.com/pachyderm/pachyderm/src/server/pfs/pretty"
	"github.com/pachyderm/pachyderm/src/server/pkg/cmdutil"

	"github.com/spf13/cobra"
)

// Cmds returns a slice containing pfs commands.
func Cmds(address string, noMetrics *bool) []*cobra.Command {
	metrics := !*noMetrics

	repo := &cobra.Command{
		Use:   "repo",
		Short: "Docs for repos.",
		Long: `Repos, short for repository, are the top level data object in Pachyderm.

	Repos are created with create-repo.`,
		Run: cmdutil.RunFixedArgs(0, func(args []string) error {
			return nil
		}),
	}

	createRepo := &cobra.Command{
		Use:   "create-repo repo-name",
		Short: "Create a new repo.",
		Long:  "Create a new repo.",
		Run: cmdutil.RunFixedArgs(1, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			return client.CreateRepo(args[0])
		}),
	}

	inspectRepo := &cobra.Command{
		Use:   "inspect-repo repo-name",
		Short: "Return info about a repo.",
		Long:  "Return info about a repo.",
		Run: cmdutil.RunFixedArgs(1, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			repoInfo, err := client.InspectRepo(args[0])
			if err != nil {
				return err
			}
			if repoInfo == nil {
				return fmt.Errorf("repo %s not found", args[0])
			}
			return pretty.PrintDetailedRepoInfo(repoInfo)
		}),
	}

	var listRepoProvenance cmdutil.RepeatedStringArg
	listRepo := &cobra.Command{
		Use:   "list-repo",
		Short: "Return all repos.",
		Long:  "Reutrn all repos.",
		Run: cmdutil.RunFixedArgs(0, func(args []string) error {
			c, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			repoInfos, err := c.ListRepo(listRepoProvenance)
			if err != nil {
				return err
			}
			writer := tabwriter.NewWriter(os.Stdout, 20, 1, 3, ' ', 0)
			pretty.PrintRepoHeader(writer)
			for _, repoInfo := range repoInfos {
				pretty.PrintRepoInfo(writer, repoInfo)
			}
			return writer.Flush()
		}),
	}
	listRepo.Flags().VarP(&listRepoProvenance, "provenance", "p", "list only repos with the specified repos provenance")

	var force bool
	deleteRepo := &cobra.Command{
		Use:   "delete-repo repo-name",
		Short: "Delete a repo.",
		Long:  "Delete a repo.",
		Run: cmdutil.RunFixedArgs(1, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			return client.DeleteRepo(args[0], force)
		}),
	}
	deleteRepo.Flags().BoolVarP(&force, "force", "f", false, "remove the repo regardless of errors; use with care")

	commit := &cobra.Command{
		Use:   "commit",
		Short: "Docs for commits.",
		Long: `Commits are atomic transactions on the content of a repo.

	Creating a commit is a multistep process:
	- start a new commit with start-commit
	- write files to it through fuse or with put-file
	- finish the new commit with finish-commit

	Commits that have been started but not finished are NOT durable storage.
	Commits become reliable (and immutable) when they are finished.

	Commits can be created with another commit as a parent.
	This layers the data in the commit over the data in the parent.`,
		Run: cmdutil.RunFixedArgs(0, func(args []string) error {
			return nil
		}),
	}

	startCommit := &cobra.Command{
		Use:   "start-commit repo-name [parent-commit | branch]",
		Short: "Start a new commit.",
		Long: `Start a new commit with parent-commit as the parent, or start a commit on the given branch; if the branch does not exist, it will be created.

	Examples:

	# Start a commit in repo "foo" on branch "bar"
	$ pachctl start-commit foo bar

	# Start a commit with master/3 as the parent in repo foo
	$ pachctl start-commit foo master/3
	`,
		Run: cmdutil.RunBoundedArgs(1, 2, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			var parent string
			if len(args) == 2 {
				parent = args[1]
			}
			commit, err := client.StartCommit(args[0], parent)
			if err != nil {
				return err
			}
			fmt.Println(commit.ID)
			return nil
		}),
	}

	finishCommit := &cobra.Command{
		Use:   "finish-commit repo-name commit-id",
		Short: "Finish a started commit.",
		Long:  "Finish a started commit. Commit-id must be a writeable commit.",
		Run: cmdutil.RunFixedArgs(2, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			return client.FinishCommit(args[0], args[1])
		}),
	}

	inspectCommit := &cobra.Command{
		Use:   "inspect-commit repo-name commit-id",
		Short: "Return info about a commit.",
		Long:  "Return info about a commit.",
		Run: cmdutil.RunFixedArgs(2, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			commitInfo, err := client.InspectCommit(args[0], args[1])
			if err != nil {
				return err
			}
			if commitInfo == nil {
				return fmt.Errorf("commit %s not found", args[1])
			}
			return pretty.PrintDetailedCommitInfo(commitInfo)
		}),
	}

	deleteCommit := &cobra.Command{
		Use:   "delete-commit repo-name commit-id",
		Short: "Delete a commit.",
		Long:  "Delete a commit.  The commit needs to be 1) open and 2) the head of a branch.",
		Run: cmdutil.RunFixedArgs(2, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			fmt.Printf("delete-commit is a beta feature; specifically, it may race with concurrent start-commit on the same branch.  Are you sure you want to proceed? yN\n")
			r := bufio.NewReader(os.Stdin)
			bytes, err := r.ReadBytes('\n')
			if err != nil {
				return err
			}
			if bytes[0] == 'y' || bytes[0] == 'Y' {
				if err := client.DeleteCommit(args[0], args[1]); err != nil {
					return err
				}
			}
			return nil
		}),
	}

	var from string
	var number int
	listCommit := &cobra.Command{
		Use:   "list-commit repo-name",
		Short: "Return all commits on a set of repos.",
		Long: `Return all commits on a set of repos.

	Examples:

	# return commits in repo "foo"
	$ pachctl list-commit foo

	# return commits in repo "foo" on branch "master"
	$ pachctl list-commit foo master

	# return the last 20 commits in repo "foo" on branch "master"
	$ pachctl list-commit foo master -n 20

	# return commits that are the ancestors of XXX
	$ pachctl list-commit foo XXX

	# return commits in repo "foo" since commit XXX
	$ pachctl list-commit foo master --from XXX
	`,
		Run: cmdutil.RunBoundedArgs(1, 2, func(args []string) (retErr error) {
			c, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}

			var to string
			if len(args) == 2 {
				to = args[1]
			}

			commitInfos, err := c.ListCommit(args[0], to, from, uint64(number))
			if err != nil {
				return err
			}

			writer := tabwriter.NewWriter(os.Stdout, 20, 1, 3, ' ', 0)
			pretty.PrintCommitInfoHeader(writer)
			for _, commitInfo := range commitInfos {
				pretty.PrintCommitInfo(writer, commitInfo)
			}
			return writer.Flush()
		}),
	}
	listCommit.Flags().StringVarP(&from, "from", "f", "", "list all commits since this commit")
	listCommit.Flags().IntVarP(&number, "number", "n", 0, "list only this many commits; if set to zero, list all commits")

	var repos cmdutil.RepeatedStringArg
	flushCommit := &cobra.Command{
		Use:   "flush-commit commit [commit ...]",
		Short: "Wait for all commits caused by the specified commits to finish and return them.",
		Long: `Wait for all commits caused by the specified commits to finish and return them.

	Examples:

	# return commits caused by foo/master/1 and bar/master/2
	$ pachctl flush-commit foo/master/1 bar/master/2

	# return commits caused by foo/master/1 leading to repos bar and baz
	$ pachctl flush-commit foo/master/1 -r bar -r baz

	`,
		Run: cmdutil.Run(func(args []string) error {
			commits, err := cmdutil.ParseCommits(args)
			if err != nil {
				return err
			}

			c, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}

			var toRepos []*pfsclient.Repo
			for _, repoName := range repos {
				toRepos = append(toRepos, client.NewRepo(repoName))
			}

			commitInfos, err := c.FlushCommit(commits, toRepos)
			if err != nil {
				return err
			}

			writer := tabwriter.NewWriter(os.Stdout, 20, 1, 3, ' ', 0)
			pretty.PrintCommitInfoHeader(writer)
			for _, commitInfo := range commitInfos {
				pretty.PrintCommitInfo(writer, commitInfo)
			}
			return writer.Flush()
		}),
	}
	flushCommit.Flags().VarP(&repos, "repos", "r", "Wait only for commits leading to a specific set of repos")

	listBranch := &cobra.Command{
		Use:   "list-branch repo-name",
		Short: "Return all branches on a repo.",
		Long:  "Return all branches on a repo.",
		Run: cmdutil.RunFixedArgs(1, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			branches, err := client.ListBranch(args[0])
			if err != nil {
				return err
			}
			writer := tabwriter.NewWriter(os.Stdout, 20, 1, 3, ' ', 0)
			pretty.PrintBranchHeader(writer)
			for _, branch := range branches {
				pretty.PrintBranch(writer, branch)
			}
			return writer.Flush()
		}),
	}

	file := &cobra.Command{
		Use:   "file",
		Short: "Docs for files.",
		Long: `Files are the lowest level data object in Pachyderm.

	Files can be written to started (but not finished) commits with put-file.
	Files can be read from finished commits with get-file.`,
		Run: cmdutil.RunFixedArgs(0, func(args []string) error {
			return nil
		}),
	}

	var filePaths []string
	var recursive bool
	var commitFlag bool
	var inputFile string
	putFile := &cobra.Command{
		Use:   "put-file repo-name commit-id path/to/file/in/pfs",
		Short: "Put a file into the filesystem.",
		Long: `Put-file supports a number of ways to insert data into pfs:
	Put data from stdin as repo/commit/path:
	echo "data" | pachctl put-file repo commit path

	Start a new commmit on branch, put data from stdin as repo/branch/path and
	finish the commit:
	echo "data" | pachctl put-file -c repo branch path

	Put a file from the local filesystem as repo/commit/path:
	pachctl put-file repo commit path -f file

	Put a file from the local filesystem as repo/commit/file:
	pachctl put-file repo commit -f file

	Put the contents of a directory as repo/commit/path/dir/file:
	pachctl put-file -r repo commit path -f dir

	Put the contents of a directory as repo/commit/dir/file:
	pachctl put-file -r repo commit -f dir

	Put the data from a URL as repo/commit/path:
	pachctl put-file repo commit path -f http://host/path

	Put the data from a URL as repo/commit/path:
	pachctl put-file repo commit -f http://host/path

	Put several files or URLs that are listed in file.
	Files and URLs should be newline delimited.
	pachctl put-file repo commit -i file

	Put several files or URLs that are listed at URL.
	NOTE this URL can reference local files, so it could cause you to put sensitive
	files into your Pachyderm cluster.
	pachctl put-file repo commit -i http://host/path
	`,
		Run: cmdutil.RunBoundedArgs(2, 3, func(args []string) (retErr error) {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			repoName := args[0]
			commitID := args[1]
			var path string
			if len(args) == 3 {
				path = args[2]
			}
			var sources []string
			if inputFile != "" {
				var r io.Reader
				if inputFile == "-" {
					r = os.Stdin
				} else if url, err := url.Parse(inputFile); err == nil && url.Scheme != "" {
					resp, err := http.Get(url.String())
					if err != nil {
						return err
					}
					defer func() {
						if err := resp.Body.Close(); err != nil && retErr == nil {
							retErr = err
						}
					}()
					r = resp.Body
				} else {
					inputFile, err := os.Open(inputFile)
					if err != nil {
						return err
					}
					defer func() {
						if err := inputFile.Close(); err != nil && retErr == nil {
							retErr = err
						}
					}()
					r = inputFile
				}
				// scan line by line
				scanner := bufio.NewScanner(r)
				for scanner.Scan() {
					if filePath := scanner.Text(); filePath != "" {
						sources = append(sources, filePath)
					}
				}
			} else {
				sources = filePaths
			}
			var eg errgroup.Group
			for _, source := range sources {
				source := source
				if len(args) == 2 {
					// The user has not specific a path so we use source as path.
					if source == "-" {
						return fmt.Errorf("no filename specified")
					}
					eg.Go(func() error {
						return putFileHelper(client, repoName, commitID, joinPaths("", source), source, recursive)
					})
				} else if len(sources) == 1 && len(args) == 3 {
					// We have a single source and the user has specified a path,
					// we use the path and ignore source (in terms of nasrc/server/pps/cmds/cmds.goming the file).
					eg.Go(func() error { return putFileHelper(client, repoName, commitID, path, source, recursive) })
				} else if len(sources) > 1 && len(args) == 3 {
					// We have multiple sources and the user has specified a path,
					// we use that path as a prefix for the filepaths.
					eg.Go(func() error {
						return putFileHelper(client, repoName, commitID, joinPaths(path, source), source, recursive)
					})
				}
			}
			return eg.Wait()
		}),
	}
	putFile.Flags().StringSliceVarP(&filePaths, "file", "f", []string{"-"}, "The file to be put, it can be a local file or a URL.")
	putFile.Flags().StringVarP(&inputFile, "input-file", "i", "", "Read filepaths or URLs from a file.  If - is used, paths are read from the standard input.")
	putFile.Flags().BoolVarP(&recursive, "recursive", "r", false, "Recursively put the files in a directory.")
	putFile.Flags().BoolVarP(&commitFlag, "commit", "c", false, "Start and finish the commit in addition to putting data.")

	getFile := &cobra.Command{
		Use:   "get-file repo-name commit-id path/to/file",
		Short: "Return the contents of a file.",
		Long:  "Return the contents of a file.",
		Run: cmdutil.RunFixedArgs(3, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			return client.GetFile(args[0], args[1], args[2], 0, 0, os.Stdout)
		}),
	}

	inspectFile := &cobra.Command{
		Use:   "inspect-file repo-name commit-id path/to/file",
		Short: "Return info about a file.",
		Long:  "Return info about a file.",
		Run: cmdutil.RunFixedArgs(3, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			fileInfo, err := client.InspectFile(args[0], args[1], args[2])
			if err != nil {
				return err
			}
			if fileInfo == nil {
				return fmt.Errorf("file %s not found", args[2])
			}
			return pretty.PrintDetailedFileInfo(fileInfo)
		}),
	}

	var recurse bool
	var fast bool
	listFile := &cobra.Command{
		Use:   "list-file repo-name commit-id path/to/dir",
		Short: "Return the files in a directory.",
		Long:  "Return the files in a directory.",
		Run: cmdutil.RunBoundedArgs(2, 3, func(args []string) error {
			if fast && recurse {
				return fmt.Errorf("you may only provide either --fast or --recurse, but not both")
			}

			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			var path string
			if len(args) == 3 {
				path = args[2]
			}
			var fileInfos []*pfsclient.FileInfo
			if fast {
				fileInfos, err = client.ListFile(args[0], args[1], path)
			} else {
				fileInfos, err = client.ListFile(args[0], args[1], path)
			}
			if err != nil {
				return err
			}
			writer := tabwriter.NewWriter(os.Stdout, 20, 1, 3, ' ', 0)
			pretty.PrintFileInfoHeader(writer)
			for _, fileInfo := range fileInfos {
				pretty.PrintFileInfo(writer, fileInfo)
			}
			return writer.Flush()
		}),
	}
	listFile.Flags().BoolVar(&recurse, "recurse", false, "if recurse is true, compute and display the sizes of directories")
	listFile.Flags().BoolVar(&fast, "fast", false, "if fast is true, don't compute the sizes of files; this makes list-file faster")

	deleteFile := &cobra.Command{
		Use:   "delete-file repo-name commit-id path/to/file",
		Short: "Delete a file.",
		Long:  "Delete a file.",
		Run: cmdutil.RunFixedArgs(3, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "user")
			if err != nil {
				return err
			}
			return client.DeleteFile(args[0], args[1], args[2])
		}),
	}

	var debug bool
	var allCommits bool
	mount := &cobra.Command{
		Use:   "mount path/to/mount/point",
		Short: "Mount pfs locally. This command blocks.",
		Long:  "Mount pfs locally. This command blocks.",
		Run: cmdutil.RunFixedArgs(1, func(args []string) error {
			client, err := client.NewMetricsClientFromAddress(address, metrics, "fuse")
			if err != nil {
				return err
			}
			go func() { client.KeepConnected(nil) }()
			mounter := fuse.NewMounter(address, client)
			mountPoint := args[0]
			ready := make(chan bool)
			go func() {
				<-ready
				fmt.Println("Filesystem mounted, CTRL-C to exit.")
			}()
			err = mounter.Mount(mountPoint, nil, ready, debug, false)
			if err != nil {
				return err
			}
			return nil
		}),
	}
	mount.Flags().BoolVarP(&debug, "debug", "d", false, "Turn on debug messages.")
	mount.Flags().BoolVarP(&allCommits, "all-commits", "a", false, "Show archived and cancelled commits.")

	var all bool
	unmount := &cobra.Command{
		Use:   "unmount path/to/mount/point",
		Short: "Unmount pfs.",
		Long:  "Unmount pfs.",
		Run: cmdutil.RunBoundedArgs(0, 1, func(args []string) error {
			if len(args) == 1 {
				return syscall.Unmount(args[0], 0)
			}

			if all {
				stdin := strings.NewReader(`
	mount | grep pfs:// | cut -f 3 -d " "
	`)
				var stdout bytes.Buffer
				if err := cmdutil.RunIO(cmdutil.IO{
					Stdin:  stdin,
					Stdout: &stdout,
					Stderr: os.Stderr,
				}, "sh"); err != nil {
					return err
				}
				scanner := bufio.NewScanner(&stdout)
				var mounts []string
				for scanner.Scan() {
					mounts = append(mounts, scanner.Text())
				}
				if len(mounts) == 0 {
					fmt.Println("No mounts found.")
					return nil
				}
				fmt.Printf("Unmount the following filesystems? yN\n")
				for _, mount := range mounts {
					fmt.Printf("%s\n", mount)
				}
				r := bufio.NewReader(os.Stdin)
				bytes, err := r.ReadBytes('\n')
				if err != nil {
					return err
				}
				if bytes[0] == 'y' || bytes[0] == 'Y' {
					for _, mount := range mounts {
						if err := syscall.Unmount(mount, 0); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}),
	}
	unmount.Flags().BoolVarP(&all, "all", "a", false, "unmount all pfs mounts")

	var result []*cobra.Command
	result = append(result, repo)
	result = append(result, createRepo)
	result = append(result, inspectRepo)
	result = append(result, listRepo)
	result = append(result, deleteRepo)
	result = append(result, commit)
	result = append(result, startCommit)
	result = append(result, finishCommit)
	result = append(result, inspectCommit)
	result = append(result, deleteCommit)
	result = append(result, listCommit)
	result = append(result, flushCommit)
	result = append(result, listBranch)
	result = append(result, file)
	result = append(result, putFile)
	result = append(result, getFile)
	result = append(result, inspectFile)
	result = append(result, listFile)
	result = append(result, deleteFile)
	result = append(result, mount)
	result = append(result, unmount)
	return result
}

func parseCommitMounts(args []string) []*fuse.CommitMount {
	var result []*fuse.CommitMount
	for _, arg := range args {
		commitMount := &fuse.CommitMount{Commit: client.NewCommit("", "")}
		repo, commitAlias := path.Split(arg)
		commitMount.Commit.Repo.Name = path.Clean(repo)
		split := strings.Split(commitAlias, ":")
		if len(split) > 0 {
			commitMount.Commit.ID = split[0]
		}
		if len(split) > 1 {
			commitMount.Alias = split[1]
		}
		result = append(result, commitMount)
	}
	return result
}

func putFileHelper(client *client.APIClient, repo, commit, path, source string, recursive bool) (retErr error) {
	if source == "-" {
		_, err := client.PutFile(repo, commit, path, os.Stdin)
		return err
	}
	// try parsing the filename as a url, if it is one do a PutFileURL
	if url, err := url.Parse(source); err == nil && url.Scheme != "" {
		return client.PutFileURL(repo, commit, path, url.String(), recursive)
	}
	if recursive {
		var eg errgroup.Group
		filepath.Walk(source, func(filePath string, info os.FileInfo, err error) error {
			if info.IsDir() {
				return nil
			}
			eg.Go(func() error {
				return putFileHelper(client, repo, commit, filepath.Join(path, strings.TrimPrefix(filePath, source)), filePath, false)
			})
			return nil
		})
		return eg.Wait()
	}
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil && retErr == nil {
			retErr = err
		}
	}()
	_, err = client.PutFile(repo, commit, path, f)
	return err
}

func joinPaths(prefix, filePath string) string {
	if url, err := url.Parse(filePath); err == nil && url.Scheme != "" {
		return filepath.Join(prefix, strings.TrimPrefix(url.Path, "/"))
	}
	return filepath.Join(prefix, filePath)
}
