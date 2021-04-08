// +build integration

package tests

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"get.porter.sh/porter/pkg/porter"
)

const wantHash = "7c2da507a73a034c9c4f82c760c3e7111ceefaf228ff440836d6f07823bd93df"

func TestArchive(t *testing.T) {
	t.Parallel()

	p := porter.NewTestPorter(t)
	p.SetupIntegrationTest()
	defer p.CleanupIntegrationTest()
	p.Debug = false

	bundleName := p.AddTestBundleDir("../build/testdata/bundles/mysql", false)
	reference := fmt.Sprintf("localhost:5000/archive-test-%s:v0.1.3", bundleName)

	// Currently, archive requires the bundle to already be published.
	// https://github.com/getporter/porter/issues/697
	publishOpts := porter.PublishOptions{}
	publishOpts.Reference = reference
	err := publishOpts.Validate(p.Context)
	require.NoError(p.T(), err, "validation of publish opts for bundle failed")

	err = p.Publish(publishOpts)
	require.NoError(p.T(), err, "publish of bundle failed")

	// Archive bundle
	archiveOpts := porter.ArchiveOptions{}
	archiveOpts.Reference = reference
	err = archiveOpts.Validate([]string{"mybuns.tgz"}, p.Porter)
	require.NoError(p.T(), err, "validation of archive opts for bundle failed")

	err = p.Archive(archiveOpts)
	require.NoError(p.T(), err, "archival of bundle failed")

	info, err := p.FileSystem.Stat("mybuns.tgz")
	require.NoError(p.T(), err)
	require.Equal(p.T(), os.FileMode(0644), info.Mode())

	// Check to be sure the shasum matches expected
	require.Equal(p.T(), wantHash, getHash(p, "mybuns.tgz"), "shasum of archive does not match expected")

	// Publish bundle from archive, with new reference
	publishFromArchiveOpts := porter.PublishOptions{
		ArchiveFile: "mybuns.tgz",
		BundlePullOptions: porter.BundlePullOptions{
			Reference: fmt.Sprintf("localhost:5000/archived-%s:v0.1.3", bundleName),
		},
	}
	err = publishFromArchiveOpts.Validate(p.Context)
	require.NoError(p.T(), err, "validation of publish opts for bundle failed")

	err = p.Publish(publishFromArchiveOpts)
	require.NoError(p.T(), err, "publish of bundle from archive failed")
}

func getHash(p *porter.TestPorter, path string) string {
	f, err := p.FileSystem.Open(path)
	require.NoError(p.T(), err, "opening archive failed")
	defer f.Close()

	h := sha256.New()
	_, err = io.Copy(h, f)
	require.NoError(p.T(), err, "hashing of archive failed")

	return fmt.Sprintf("%x", h.Sum(nil))
}
