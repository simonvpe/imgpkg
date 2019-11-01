package cmd

import (
	"github.com/spf13/cobra"
)

type FileFlags struct {
	Files      []string
	RawTarFile string
}

func (s *FileFlags) Set(cmd *cobra.Command) {
	cmd.Flags().StringSliceVarP(&s.Files, "file", "f", nil, "Set file (format: /tmp/foo, -) (can be specified multiple times)")
	cmd.Flags().StringVar(&s.RawTarFile, "file-raw-tar", "", "Set raw tar file (format: /tmp/foo.tgz, -)")
}
