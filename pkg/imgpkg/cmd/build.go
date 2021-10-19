// Copyright 2020 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/cppforlife/go-cli-ui/ui"
	regname "github.com/google/go-containerregistry/pkg/name"
	"github.com/k14s/imgpkg/pkg/imgpkg/bundle"
	ctlimg "github.com/k14s/imgpkg/pkg/imgpkg/image"
	ctlimgset "github.com/k14s/imgpkg/pkg/imgpkg/imageset"
	"github.com/k14s/imgpkg/pkg/imgpkg/imagetar"
	"github.com/k14s/imgpkg/pkg/imgpkg/plainimage"
	"github.com/k14s/imgpkg/pkg/imgpkg/registry"
	"github.com/k14s/imgpkg/pkg/imgpkg/util"
	"github.com/spf13/cobra"
)

type BuildOptions struct {
	ui ui.UI

	ImageFlags                  ImageFlags
	BundleFlags                 BundleFlags
	FileFlags                   FileFlags
	RegistryFlags               RegistryFlags
	IncludeNonDistributableFlag IncludeNonDistributableFlag

	TarDst      string
	Concurrency int
}

func NewBuildOptions(ui ui.UI) *BuildOptions {
	return &BuildOptions{ui: ui}
}

func NewBuildCmd(o *BuildOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build files into an image stored as a tarball",
		RunE:  func(_ *cobra.Command, _ []string) error { return o.Run() },
		Example: `
  # Build bundle repo/app1-config with contents of config/ directory stored in /tmp/bundle.tar
  imgpkg build -b repo/app1-config -f config/ --to-tar /tmp/bundle.tar

  # Build image repo/app1-config with contents from multiple locations stored in /tmp/image.tar
  imgpkg build -i repo/app1-config -f config/ -f additional-config.yml --to-tar /tmp/image.tar`,
	}
	o.ImageFlags.Set(cmd)
	o.BundleFlags.Set(cmd)
	o.FileFlags.Set(cmd)
	o.RegistryFlags.Set(cmd)
	o.IncludeNonDistributableFlag.Set(cmd)

	cmd.Flags().StringVar(&o.TarDst, "to-tar", "", "Location to write a tar file containing assets")
	cmd.Flags().IntVar(&o.Concurrency, "concurrency", 5, "Concurrency")
	return cmd
}

func (bo *BuildOptions) Run() error {
	reg, err := registry.NewRegistry(bo.RegistryFlags.AsRegistryOpts())
	if err != nil {
		return err
	}

	isBundle := bo.BundleFlags.Bundle != ""
	isImage := bo.ImageFlags.Image != ""
	var repoAndDigest string

	switch {
	case isBundle && isImage:
		return fmt.Errorf("Expected only one of image or bundle")

	case !isBundle && !isImage:
		return fmt.Errorf("Expected either image or bundle")

	case isBundle:
		repoAndDigest, err = bo.buildBundle(reg)
		if err != nil {
			return err
		}

	case isImage:
		repoAndDigest, err = bo.buildImage(reg)
		if err != nil {
			return err
		}

	default:
		panic("Unreachable code")
	}

	bo.ui.BeginLinef("Built '%s'", repoAndDigest)

	return nil
}

func (bo *BuildOptions) buildBundle(registry registry.Registry) (string, error) {
	prefixedLogger := util.NewUIPrefixedWriter("build | ", bo.ui)
	levelLogger := util.NewUILevelLogger(util.LogWarn, prefixedLogger)

	bundleFileImage, err := bundle.NewContents(bo.FileFlags.Files, bo.FileFlags.ExcludedFilePaths).Build(bo.ui)
	if err != nil {
		return "", err
	}
	defer bundleFileImage.Remove()

	bundleDigest, err := bo.getDigest(bo.BundleFlags.Bundle, bundleFileImage)
	if err != nil {
		return "", err
	}

	bundleTag, err := bo.getTag(bo.BundleFlags.Bundle)
	if err != nil {
		return "", err
	}

	plainImage := plainimage.NewFetchedPlainImageWithTag(bundleDigest, bundleTag, bundleFileImage)
	rootBundle := bundle.NewBundleFromPlainImage(plainImage, registry)

	_, imageRefs, err := rootBundle.AllImagesRefs(bo.Concurrency, levelLogger)
	if err != nil {
		return "", err
	}

	unprocessedImageRefs := ctlimgset.NewUnprocessedImageRefs()
	for _, img := range imageRefs.ImageRefs() {
		unprocessedImageRefs.Add(ctlimgset.UnprocessedImageRef{DigestRef: img.PrimaryLocation()})
	}

	processedImages := ctlimgset.NewProcessedImages()
	processedImages.Add(ctlimgset.ProcessedImage{
		UnprocessedImageRef: ctlimgset.UnprocessedImageRef{
			DigestRef: rootBundle.DigestRef(),
			Tag:       rootBundle.Tag(),
			Labels: map[string]string{
				rootBundleLabelKey: "",
			}},
		DigestRef:  rootBundle.DigestRef(),
		Image:      bundleFileImage,
		ImageIndex: nil,
	})

	tarImageSet := ctlimgset.NewTarImageSet(ctlimgset.NewImageSet(bo.Concurrency, prefixedLogger), bo.Concurrency, prefixedLogger)
	includeNonDistributable := bo.IncludeNonDistributableFlag.IncludeNonDistributable

	_, err = tarImageSet.Export(unprocessedImageRefs, processedImages, bo.TarDst, registry, imagetar.NewImageLayerWriterCheck(includeNonDistributable))
	if err != nil {
		return "", err
	}

	return rootBundle.DigestRef(), nil
}

func (bo *BuildOptions) buildImage(registry registry.Registry) (string, error) {
	prefixedLogger := util.NewUIPrefixedWriter("build | ", bo.ui)

	err := bo.validateImageUserFlags()
	if err != nil {
		return "", err
	}

	imageFile, err := ctlimg.NewTarImage(bo.FileFlags.Files, bo.FileFlags.ExcludedFilePaths, InfoLog{bo.ui}).AsFileImage(map[string]string{})
	if err != nil {
		return "", err
	}

	imageDigest, err := bo.getDigest(bo.ImageFlags.Image, imageFile)
	if err != nil {
		return "", err
	}

	imageTag, err := bo.getTag(bo.ImageFlags.Image)
	if err != nil {
		return "", err
	}

	processedImages := ctlimgset.NewProcessedImages()
	processedImages.Add(ctlimgset.ProcessedImage{
		UnprocessedImageRef: ctlimgset.UnprocessedImageRef{
			DigestRef: imageDigest,
			Tag:       imageTag,
		},
		DigestRef:  imageDigest,
		Image:      imageFile,
		ImageIndex: nil,
	})

	isNonDistributable := bo.IncludeNonDistributableFlag.IncludeNonDistributable
	tarImageSet := ctlimgset.NewTarImageSet(ctlimgset.NewImageSet(bo.Concurrency, prefixedLogger), bo.Concurrency, prefixedLogger)
	_, err = tarImageSet.Export(ctlimgset.NewUnprocessedImageRefs(), processedImages, bo.TarDst, registry, imagetar.NewImageLayerWriterCheck(isNonDistributable))
	if err != nil {
		return "", err
	}

	return imageDigest, nil
}

func (bo *BuildOptions) validateImageUserFlags() error {
	isBundle, err := bundle.NewContents(bo.FileFlags.Files, bo.FileFlags.ExcludedFilePaths).PresentsAsBundle()
	if err != nil {
		return err
	}
	if isBundle {
		return fmt.Errorf("Images cannot be pushed with '.imgpkg' directories, consider using --bundle (-b) option")
	}
	return nil
}

func (bo *BuildOptions) getDigest(imageRef string, buildImage *ctlimg.FileImage) (string, error) {
	parseReference, err := regname.ParseReference(imageRef)
	if err != nil {
		return "", err
	}

	digest, err := buildImage.Digest()
	if err != nil {
		return "", err
	}

	newDigest, err := regname.NewDigest(fmt.Sprintf("%s@%s", parseReference.Context().Name(), digest.String()))
	if err != nil {
		return "", err
	}

	return newDigest.Name(), nil
}

func (bo *BuildOptions) getTag(imageRef string) (string, error) {
	uploadRef, err := regname.NewTag(imageRef, regname.WeakValidation)
	if err != nil {
		return "", fmt.Errorf("Parsing '%s': %s", imageRef, err)
	}
	return uploadRef.TagStr(), nil
}

type InfoLog struct {
	ui ui.UI
}

func (l InfoLog) Write(data []byte) (int, error) {
	l.ui.BeginLinef(string(data))
	return len(data), nil
}
