// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrVolumeNotFound = errors.New("volume not found")
	ErrBucketNotFound = errors.New("bucket not found")

	ErrVolumeIsntManaged = errors.New("volume isn't managed")
	ErrBucketIsntManaged = errors.New("bucket isn't managed")
)

func ConvertInternalErrorToGRPC(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}

	code := codes.Internal

	switch {
	case errors.Is(err, ErrBucketNotFound), errors.Is(err, ErrVolumeNotFound):
		code = codes.NotFound
	case errors.Is(err, ErrBucketIsntManaged), errors.Is(err, ErrVolumeIsntManaged):
		code = codes.InvalidArgument
	}

	return status.Error(code, err.Error())
}
