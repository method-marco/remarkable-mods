# secondary-pen-toolbar

Adds a second pen to the toolbar of the reMarkable's notes app (xochitl), with its own
tool, colour and thickness. Requires xovi with qt-resource-rebuilder.

## Background

xochitl already has a complete second pen ("Secondary pen": own pen settings, saved as
`SecondaryPen*` in `xochitl.conf`, reselected when a document is reopened). It only
shows when the toolbar's slot logic gives it a place in the main row. The toolbar keeps a
spare slot, and the second pen has the lowest priority of all tools. So on smaller
screens (Paper Pro Move: 6 slots) it's always pushed into "more tools", where it hides
itself.

This mod adds a dedicated instance of xochitl's own `SecondaryPenMenu` right after the
first pen (`Toolbar.qml`), independent of the slot logic. Everything else
(pen menu, colours, sizes, selection) is xochitl's built-in behaviour.

The toolbar doesn't count the extra button, so the row would be one button too long and
the last button (three dots) would slide under the close button. Instead of dropping a
tool, the tool buttons are made slightly narrower when needed so all of them fit.

## Install

```sh
./deploy.sh secondary-pen-toolbar      # restarts the reMarkable UI if xovi is running
./deploy.sh secondary-pen-toolbar --uninstall
```

## Notes

- Written against reMarkable OS 3.29 (Paper Pro Move). The patch targets QML files that
  may change in other OS versions; if the patch doesn't apply, xochitl just runs without it.
- If xochitl ever gives its own second pen button a slot (larger screens), that button
  is hidden so the pen doesn't appear twice.
