package vagrant

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mitchellh/packer/packer"
)

type HyveProvider struct{}

func (p *HyveProvider) KeepInputArtifact() bool {
	return false
}

func (p *HyveProvider) Process(ui packer.Ui, artifact packer.Artifact, dir string) (vagrantfile string, metadata map[string]interface{}, err error) {
	// Create the metadata
	metadata = map[string]interface{}{"provider": "xhyve"}

	diskName := artifact.State("diskName").(string)

	// Copy the disk image into the temporary directory (as box.img)
	for _, path := range artifact.Files() {
		if strings.HasSuffix(path, "/"+diskName) {
			ui.Message(fmt.Sprintf("Copying from artifact: %s", path))
			dstPath := filepath.Join(dir, "block0.img")
			if err = CopyContents(dstPath, path); err != nil {
				return
			}
		}
	}

	return
}
