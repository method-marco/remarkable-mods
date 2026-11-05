# eraser-end-uses-eraser-tool

The back of the Marker (eraser end) erases with the settings chosen in the toolbar's
**Eraser** tool: eraser type (normal eraser or area eraser) and size. Stock xochitl
ignores those and always uses a normal eraser of a fixed medium size for the eraser end.
Requires xovi with qt-resource-rebuilder.

## How it works

In `DeviceSceneView.qml`, xochitl's pen input handler hard-codes
`eraserThickness: calculateEraserThickness(2.4)` and never sets `eraserTool`. The patch
binds both to the Eraser tool's settings (`documentViewTools.eraserPen`), using the same
thickness formula xochitl uses for the toolbar eraser. To change how the eraser end
erases, pick the eraser type and size in the toolbar's Eraser menu as usual.

## Install

```sh
./deploy.sh eraser-end-uses-eraser-tool        # restarts the reMarkable UI if xovi is running
./deploy.sh eraser-end-uses-eraser-tool --uninstall
```

Written against reMarkable OS 3.29 (Paper Pro Move).
