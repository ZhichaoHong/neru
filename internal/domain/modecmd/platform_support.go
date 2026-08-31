package modecmd

import "github.com/y3owk1n/neru/internal/domain/parity"

// PlatformSupport declares, for every mode flag, the platforms on which
// writing it does something.
//
// It sits beside the descriptor table rather than inside it because it answers
// a different question from the rest of a Descriptor: the descriptors say how a
// flag is written and read, and this says where writing it means anything.
// Keeping the two in one file would still leave a flag able to be declared
// without a column, which is what TestEveryModeFlagDeclaresItsPlatformSupport
// exists to prevent
// (docs/adr/0013-parity-is-measured-in-words-not-subsystems.md).
//
// Every mode flag is supported on every platform, so there are no narrow columns
// and no notes to write. That has not always been true: --split-word and
// --strategy=vision were macOS-and-Linux until Windows grew an OCR engine, and
// both were declared here with the sentence that said why. The refusal
// --split-word still carries is about the strategy in use rather than about the
// platform - HintService rejects it for any non-vision strategy, everywhere.
func PlatformSupport() parity.Declaration {
	return parity.Everywhere(parity.KindModeFlag,
		FlagSplitWord.String(),
		FlagAction.String(),
		FlagModifier.String(),
		FlagOnExit.String(),
		FlagRepeat.String(),
		FlagToggle.String(),
		FlagSearch.String(),
		FlagHideOnEmptySearch.String(),
		FlagRole.String(),
		FlagText.String(),
		FlagStrategy.String(),
		FlagLabelDirection.String(),
		FlagZoomToDepth.String(),
		FlagCursorSelectionMode.String(),
	)
}
