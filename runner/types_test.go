package runner

import (
	"testing"

	"github.com/cloudbase/garm-provider-common/params"
	"github.com/stretchr/testify/require"
)

func TestSupportedOSTypeIncludesMacOS(t *testing.T) {
	require.True(t, IsSupportedOSType(params.Linux))
	require.True(t, IsSupportedOSType(params.Windows))
	require.True(t, IsSupportedOSType(params.MacOS))
	require.False(t, IsSupportedOSType(params.Unknown))
}
