//go:build !unix

package capability

import "context"

func discoverLocalFilesGuarded(
	ctx context.Context,
	guardedRoot, absoluteRoot string,
	patterns, excludes []string,
	includeHidden bool,
	maxScanned, maxMatches int,
	afterPath string,
	afterRootOpen func(),
) ([]fileCandidate, int, bool, error) {
	if err := ensureGuardedMutationPath(absoluteRoot, guardedRoot); err != nil {
		return nil, 0, false, err
	}
	if afterRootOpen != nil {
		afterRootOpen()
	}
	if err := ensureGuardedMutationPath(absoluteRoot, guardedRoot); err != nil {
		return nil, 0, false, err
	}
	return discoverLocalFilesPath(
		ctx, absoluteRoot, absoluteRoot, patterns, excludes, includeHidden,
		maxScanned, maxMatches, afterPath, nil,
	)
}
