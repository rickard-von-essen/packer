package hyve

import (
	"fmt"
	"path/filepath"

	"github.com/mitchellh/multistep"
	"github.com/mitchellh/packer/packer"
	"github.com/mitchellh/packer/template/interpolate"
)

// stepRun runs the virtual machine
type stepRun struct {
	BootDrive string
	Message   string
}

type hyveArgsTemplateData struct {
	HTTPIP    string
	HTTPPort  uint
	HTTPDir   string
	OutputDir string
	Name      string
}

func (s *stepRun) Run(state multistep.StateBag) multistep.StepAction {
	driver := state.Get("driver").(Driver)
	ui := state.Get("ui").(packer.Ui)

	ui.Say(s.Message)

	command, err := getCommandArgs(s.BootDrive, state)
	if err != nil {
		err := fmt.Errorf("Error processing HyveArgs: %s", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	if err := driver.Hyve(command...); err != nil {
		err := fmt.Errorf("Error launching VM: %s", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	//state.Put("tty_dev", driver.TTY)

	return multistep.ActionContinue
}

func (s *stepRun) Cleanup(state multistep.StateBag) {
	driver := state.Get("driver").(Driver)
	ui := state.Get("ui").(packer.Ui)

	if err := driver.Stop(); err != nil {
		ui.Error(fmt.Sprintf("Error shutting down VM: %s", err))
	}
}

func getCommandArgs(bootDrive string, state multistep.StateBag) ([]string, error) {
	config := state.Get("config").(*Config)
	isoPath := state.Get("iso_path").(string)
	diskFile := state.Get("disk_filename").(string)
	//sshHostPort := state.Get("sshHostPort").(uint)
	ui := state.Get("ui").(packer.Ui)

	//vnc := fmt.Sprintf("0.0.0.0:%d", vncPort-5900)
	//vmName := config.VMName
	diskPath := filepath.Join(config.OutputDir, diskFile)

	defaultArgs := make([]string, 0)

	defaultArgs = append(defaultArgs, "-A") // ACPI
	if len(config.MemorySize) != 0 {
		defaultArgs = append(defaultArgs, "-m", config.MemorySize)
	}
	if config.Cpus != 0 {
		defaultArgs = append(defaultArgs, "-c", fmt.Sprintf("%d", config.Cpus))
	}
	defaultArgs = append(defaultArgs, []string{"-s", "0:0,hostbridge", "-s", "31,lpc"}...) // PCI dev
	// Connect the serial port com1 to a tty
	defaultArgs = append(defaultArgs, []string{"-l", "com1,autopty"}...)
	// Net
	defaultArgs = append(defaultArgs, []string{"-s", "2:0,virtio-net"}...)
	// ISO
	defaultArgs = append(defaultArgs, []string{"-s", fmt.Sprintf("3,ahci-cd,%s", isoPath)}...)
	// HDD
	defaultArgs = append(defaultArgs, []string{"-s", fmt.Sprintf("4,virtio-blk,%s", diskPath)}...)
	// UUID ??

	// Hardcoded TinyCore Linux
	//defaultArgs = append(defaultArgs, []string{"-f", "kexec,/tmp/tc/vmlinuz,/tmp/tc/initrd.gz,\"earlyprintk=serial console=ttyS0\""}...)
	// Hardcoded Ubuntu Linux
	// defaultArgs = append(defaultArgs, []string{"-f", "kexec,ubuntu/boot/vmlinuz-3.19.0-25-generic,ubuntu/boot/initrd.img-3.19.0-25-generic,\"earlyprintk=serial console=ttyS0\""}...)
	defaultArgs = append(defaultArgs, []string{"-f", fmt.Sprintf("kexec,%s,%s,\"%s\"", config.LinuxKernel, config.LinuxInitrd, config.KernelArgs)}...)
	/*
			if !config.DiskImage {
			defaultArgs["-cdrom"] = isoPath
		}
	*/

	/* /var/log/system.log

	Sep 11 15:02:09 Rickards-MacBook-Pro bootpd[50119]: DHCP DISCOVER [bridge100]: 1,1e:f1:42:7:cf:32 <ubuntu>
	Sep 11 15:02:09 Rickards-MacBook-Pro bootpd[50119]: OFFER sent <no hostname> 192.168.64.4 pktsize 300
	Sep 11 15:02:09 Rickards-MacBook-Pro bootpd[50119]: DHCP REQUEST [bridge100]: 1,1e:f1:42:7:cf:32 <ubuntu>
	Sep 11 15:02:09 Rickards-MacBook-Pro bootpd[50119]: ACK sent <no hostname> 192.168.64.4 pktsize 300

	*/

	outArgs := defaultArgs
	if len(config.HyveArgs) > 0 {
		ui.Say("Overriding defaults bhyve/xhyve arguments with HyveArgs...")

		httpPort := state.Get("http_port").(uint)
		ctx := config.ctx
		ctx.Data = hyveArgsTemplateData{
			"10.0.2.2",
			httpPort,
			config.HTTPDir,
			config.OutputDir,
			config.VMName,
		}

		for i := range config.HyveArgs {
			newHyveArg, err := interpolate.Render(config.HyveArgs[i], &ctx)
			if err != nil {
				return nil, err
			}
			outArgs = append(outArgs, newHyveArg)
		}
	}

	return outArgs, nil
}
