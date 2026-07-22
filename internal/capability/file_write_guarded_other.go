//go:build !unix

package capability

import "context"

func writeLocalFileAtomicGuarded(ctx context.Context, request WriteFileRequest, options localFileWriteOptions) (FileResult, error) {
	return writeLocalFileAtomicPath(ctx, request, options)
}
