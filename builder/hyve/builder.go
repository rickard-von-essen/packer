package hyve

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mitchellh/multistep"
	"github.com/mitchellh/packer/common"
	"github.com/mitchellh/packer/helper/communicator"
	"github.com/mitchellh/packer/helper/config"
	"github.com/mitchellh/packer/packer"
	"github.com/mitchellh/packer/template/interpolate"
)

const BuilderId = "rickard-von-essen.hyve"

type Builder struct {
	config Config
	runner multistep.Runner
}

type Config struct {
	common.PackerConfig `mapstructure:",squash"`
	Comm                communicator.Config `mapstructure:",squash"`

	BootCommand []string `mapstructure:"boot_command"`
	Cpus        uint     `mapstructure:"cpus"`
	DiskSize    uint     `mapstructure:"disk_size"`
	//FloppyFiles   []string `mapstructure:"floppy_files"`
	Format string `mapstructure:"format"`
	//DiskImage       bool     `mapstructure:"disk_image"`
	HTTPDir         string   `mapstructure:"http_directory"`
	HTTPPortMin     uint     `mapstructure:"http_port_min"`
	HTTPPortMax     uint     `mapstructure:"http_port_max"`
	ISOChecksum     string   `mapstructure:"iso_checksum"`
	ISOChecksumType string   `mapstructure:"iso_checksum_type"`
	ISOUrls         []string `mapstructure:"iso_urls"`
	LinuxKernel     string   `mapstructure:"linux_kernel"`
	LinuxInitrd     string   `mapstructure:"linux_initrd"`
	KernelArgs      string   `mapstructure:"kernel_arguments"`
	MemorySize      string   `mapstructure:"memory_size"`
	NetDevice       string   `mapstructure:"net_device"`
	OutputDir       string   `mapstructure:"output_directory"`
	HyveArgs        []string `mapstructure:"hyveargs"`
	HyveBinary      string   `mapstructure:"hyve_binary"`
	ShutdownCommand string   `mapstructure:"shutdown_command"`
	VMName          string   `mapstructure:"vm_name"`

	RawBootWait        string `mapstructure:"boot_wait"`
	RawSingleISOUrl    string `mapstructure:"iso_url"`
	RawShutdownTimeout string `mapstructure:"shutdown_timeout"`

	bootWait        time.Duration ``
	shutdownTimeout time.Duration ``
	ctx             interpolate.Context
}

func (b *Builder) Prepare(raws ...interface{}) ([]string, error) {
	err := config.Decode(&b.config, &config.DecodeOpts{
		Interpolate:        true,
		InterpolateContext: &b.config.ctx,
		InterpolateFilter: &interpolate.RenderFilter{
			Exclude: []string{
				"boot_command",
				"hyveargs",
			},
		},
	}, raws...)
	if err != nil {
		return nil, err
	}

	if b.config.DiskSize == 0 {
		b.config.DiskSize = 40000
	}

	if b.config.HTTPPortMin == 0 {
		b.config.HTTPPortMin = 8000
	}

	if b.config.HTTPPortMax == 0 {
		b.config.HTTPPortMax = 9000
	}

	if b.config.OutputDir == "" {
		b.config.OutputDir = fmt.Sprintf("output-%s", b.config.PackerBuildName)
	}

	if b.config.KernelArgs == "" {
		b.config.KernelArgs = "earlyprintk=serial console=ttyS0"

	}
	if b.config.HyveBinary == "" {
		switch runtime.GOOS {
		case "darwin":
			b.config.HyveBinary = "xhyve"

		case "freebsd":
			b.config.HyveBinary = "bhyve"
		}
	}

	if b.config.RawBootWait == "" {
		b.config.RawBootWait = "10s"
	}

	if b.config.VMName == "" {
		b.config.VMName = fmt.Sprintf("packer-%s", b.config.PackerBuildName)
	}

	if b.config.Format == "" {
		b.config.Format = "raw" // TODO change to qcow2 when supported
	}

	// if b.config.FloppyFiles == nil {
	// 	b.config.FloppyFiles = make([]string, 0)
	// }

	if b.config.NetDevice == "" {
		b.config.NetDevice = "virtio-net"
	}

	var errs *packer.MultiError
	warnings := make([]string, 0)

	if runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" {
		errs = packer.MultiErrorAppend(
			errs, errors.New("unsupported OS, only bhyve on FreeBSD and xhyve on Darwin (OS X) are supported!"))
	}

	if es := b.config.Comm.Prepare(&b.config.ctx); len(es) > 0 {
		errs = packer.MultiErrorAppend(errs, es...)
	}

	if !(b.config.Format == "qcow2" || b.config.Format == "raw") {
		errs = packer.MultiErrorAppend(
			errs, errors.New("invalid format, only 'qcow2' or 'raw' are allowed"))
	}

	if b.config.HTTPPortMin > b.config.HTTPPortMax {
		errs = packer.MultiErrorAppend(
			errs, errors.New("http_port_min must be less than http_port_max"))
	}

	if b.config.ISOChecksumType == "" {
		errs = packer.MultiErrorAppend(
			errs, errors.New("The iso_checksum_type must be specified."))
	} else {
		b.config.ISOChecksumType = strings.ToLower(b.config.ISOChecksumType)
		if b.config.ISOChecksumType != "none" {
			if b.config.ISOChecksum == "" {
				errs = packer.MultiErrorAppend(
					errs, errors.New("Due to large file sizes, an iso_checksum is required"))
			} else {
				b.config.ISOChecksum = strings.ToLower(b.config.ISOChecksum)
			}

			if h := common.HashForType(b.config.ISOChecksumType); h == nil {
				errs = packer.MultiErrorAppend(
					errs,
					fmt.Errorf("Unsupported checksum type: %s", b.config.ISOChecksumType))
			}
		}
	}

	if b.config.RawSingleISOUrl == "" && len(b.config.ISOUrls) == 0 {
		errs = packer.MultiErrorAppend(
			errs, errors.New("One of iso_url or iso_urls must be specified."))
	} else if b.config.RawSingleISOUrl != "" && len(b.config.ISOUrls) > 0 {
		errs = packer.MultiErrorAppend(
			errs, errors.New("Only one of iso_url or iso_urls may be specified."))
	} else if b.config.RawSingleISOUrl != "" {
		b.config.ISOUrls = []string{b.config.RawSingleISOUrl}
	}

	for i, url := range b.config.ISOUrls {
		b.config.ISOUrls[i], err = common.DownloadableURL(url)
		if err != nil {
			errs = packer.MultiErrorAppend(
				errs, fmt.Errorf("Failed to parse iso_url %d: %s", i+1, err))
		}
	}

	if !b.config.PackerForce {
		if _, err := os.Stat(b.config.OutputDir); err == nil {
			errs = packer.MultiErrorAppend(
				errs,
				fmt.Errorf("Output directory '%s' already exists. It must not exist.", b.config.OutputDir))
		}
	}

	b.config.bootWait, err = time.ParseDuration(b.config.RawBootWait)
	if err != nil {
		errs = packer.MultiErrorAppend(
			errs, fmt.Errorf("Failed parsing boot_wait: %s", err))
	}

	if b.config.RawShutdownTimeout == "" {
		b.config.RawShutdownTimeout = "5m"
	}

	b.config.shutdownTimeout, err = time.ParseDuration(b.config.RawShutdownTimeout)
	if err != nil {
		errs = packer.MultiErrorAppend(
			errs, fmt.Errorf("Failed parsing shutdown_timeout: %s", err))
	}

	if b.config.HyveArgs == nil {
		b.config.HyveArgs = make([]string, 0)
	}

	if b.config.ISOChecksumType == "none" {
		warnings = append(warnings,
			"A checksum type of 'none' was specified. Since ISO files are so big,\n"+
				"a checksum is highly recommended.")
	}

	if errs != nil && len(errs.Errors) > 0 {
		return warnings, errs
	}

	return warnings, nil
}

func (b *Builder) Run(ui packer.Ui, hook packer.Hook, cache packer.Cache) (packer.Artifact, error) {
	// Create the driver that we'll use to communicate with bhyve/xhyve
	driver, err := b.newDriver(b.config.HyveBinary)
	if err != nil {
		return nil, fmt.Errorf("Failed creating bhyve/xhyve driver: %s", err)
	}

	driver.Version()

	steps := []multistep.Step{
		&common.StepDownload{
			Checksum:     b.config.ISOChecksum,
			ChecksumType: b.config.ISOChecksumType,
			Description:  "ISO",
			ResultKey:    "iso_path",
			Url:          b.config.ISOUrls,
		},
		new(stepPrepareOutputDir),
		//&common.StepCreateFloppy{
		//	Files: b.config.FloppyFiles,
		//},
		new(stepCreateDisk),
		//new(stepCopyDisk),
		//new(stepResizeDisk),
		new(stepHTTPServer),
		new(stepRun),
		&stepBootWait{},
		&stepTypeBootCommand{},
		/*
			&communicator.StepConnect{
				Config:    &b.config.Comm,
				Host:      commHost,
				SSHConfig: sshConfig,
				SSHPort:   commPort,
			},
			new(common.StepProvision),
			new(stepShutdown),
		*/
	}

	// Setup the state bag
	state := new(multistep.BasicStateBag)
	state.Put("cache", cache)
	state.Put("config", &b.config)
	state.Put("driver", driver)
	state.Put("hook", hook)
	state.Put("ui", ui)

	// Run
	if b.config.PackerDebug {
		b.runner = &multistep.DebugRunner{
			Steps:   steps,
			PauseFn: common.MultistepDebugFn(ui),
		}
	} else {
		b.runner = &multistep.BasicRunner{Steps: steps}
	}

	b.runner.Run(state)

	// If there was an error, return that
	if rawErr, ok := state.GetOk("error"); ok {
		return nil, rawErr.(error)
	}

	// If we were interrupted or cancelled, then just exit.
	if _, ok := state.GetOk(multistep.StateCancelled); ok {
		return nil, errors.New("Build was cancelled.")
	}

	if _, ok := state.GetOk(multistep.StateHalted); ok {
		return nil, errors.New("Build was halted.")
	}

	// Compile the artifact list
	files := make([]string, 0, 5)
	visit := func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() {
			files = append(files, path)
		}

		return err
	}

	if err := filepath.Walk(b.config.OutputDir, visit); err != nil {
		return nil, err
	}

	artifact := &Artifact{
		dir:   b.config.OutputDir,
		f:     files,
		state: make(map[string]interface{}),
	}

	artifact.state["diskName"] = state.Get("disk_filename").(string)
	artifact.state["diskFormat"] = b.config.Format
	artifact.state["diskSize"] = uint64(b.config.DiskSize)

	return artifact, nil
}

func (b *Builder) Cancel() {
	if b.runner != nil {
		log.Println("Cancelling the step runner...")
		b.runner.Cancel()
	}
}

func (b *Builder) newDriver(hyveBinary string) (Driver, error) {
	hyvePath, err := exec.LookPath(hyveBinary)
	if err != nil {
		return nil, err
	}

	qemuImgPath, err := exec.LookPath("qemu-img")
	if err != nil {
		return nil, err
	}

	log.Printf("bhyve/xhyve path: %s, qemu-img path: %s", hyvePath, qemuImgPath)
	driver := &HyveDriver{
		HyvePath:    hyvePath,
		QemuImgPath: qemuImgPath,
	}

	if err := driver.Verify(); err != nil {
		return nil, err
	}

	return driver, nil
}
