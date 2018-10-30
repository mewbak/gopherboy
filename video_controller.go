package main

import (
	"fmt"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	screenWidth  = 160
	screenHeight = 144

	// The number of clock ticks that the video controller has exclusive access
	// to OAM RAM for. This happens once per scan line.
	scanLineOAMClocks = 80
	// The number of clock ticks that the video controller has exclusive access
	// to VRAM for. This happens once per scan line.
	scanLineVRAMClocks = 172
	// The number of clock ticks in between scan lines.
	horizontalBlankClocks = 204
	// The amount of clocks taken for a scan line.
	scanLineFullClocks = scanLineOAMClocks + scanLineVRAMClocks + horizontalBlankClocks
	// The number of clock ticks in between frame draws.
	verticalBlankClocks = 4560
	// The total number of clocks taken for a frame.
	fullFrameClocks = (scanLineFullClocks * screenHeight) + verticalBlankClocks
)

type drawStep int

const (
	_ drawStep = 0
	oamStep
	vramStep
	horizontalBlankStep
	verticalBlankStep
)

type videoController struct {
	window   *sdl.Window
	renderer *sdl.Renderer

	frameTick      int
	drawnScanLines int
	lcdc           lcdcConfig
	// Contains tile data, where the key is the tile ID and the value is a
	// slice of dot codes corresponding to colors.
	tiles map[uint8][]uint8
	// scrollX controls the X position of the background.
	scrollX int8
	// scrollY controls the Y position of the background.
	scrollY int8
	// bgPalette is the palette for the background.
	bgPalette map[uint8]color

	timers *timers
	env    *environment

	lastSecond time.Time
	frameCnt   int
}

func newVideoController(env *environment, timers *timers) (videoController, error) {
	var vc videoController

	vc.env = env
	vc.timers = timers

	err := sdl.Init(sdl.INIT_EVERYTHING)
	if err != nil {
		return videoController{}, fmt.Errorf("initializing SDL: %v", err)
	}

	vc.window, err = sdl.CreateWindow(
		"Gopherboy",
		sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		screenWidth, screenHeight,
		sdl.WINDOW_OPENGL)
	if err != nil {
		return videoController{}, fmt.Errorf("initializing window: %v", err)
	}

	vc.renderer, err = sdl.CreateRenderer(vc.window, -1, sdl.RENDERER_ACCELERATED)
	if err != nil {
		return videoController{}, fmt.Errorf("initializing renderer:", err)
	}

	vc.lastSecond = time.Now()

	vc.renderer.SetDrawColor(255, 255, 255, 255)

	return vc, nil
}

// tick progresses the video controller by the given number of cycles.
func (vc *videoController) tick(opTime int) {
	// Check if the LCD should be on
	if !vc.loadLCDC().lcdOn {
		return
	}

	for i := 0; i < opTime; i++ {
		if vc.frameTick == 0 {
			// Get ready for a new frame draw
			vc.renderer.Clear()
			// Read some initial values
			vc.lcdc = vc.loadLCDC()
			vc.scrollY = asSigned(vc.env.mmu.at(scrollYAddr))
		}

		// Update the LY register with the current scan line. Note that this
		// value increments even during VBlank even though new scan lines
		// aren't actually being drawn.
		currScanLine := vc.frameTick / scanLineFullClocks
		vc.env.mmu.set(lyAddr, uint8(currScanLine))

		if vc.frameTick < scanLineFullClocks*screenHeight {
			// We're still drawing scan lines
			scanLineProgress := vc.frameTick % scanLineFullClocks

			switch scanLineProgress {
			case 0:
				// We're in mode 2, OAM read mode.
				vc.setMode(vcMode2)
				// TODO(velovix): Load OAM
				// TODO(velovix): Lock OAM?
				// This is the start of this scan line, read some scan line
				// global values
				vc.scrollX = asSigned(vc.env.mmu.at(scrollXAddr))
				vc.bgPalette = vc.loadBGPalette()
			case scanLineOAMClocks:
				// We're in mode 3, OAM and VRAM transfer mode.
				vc.setMode(vcMode3)
				vc.tiles = vc.loadBGTiles()
				// TODO(velovix): Lock VRAM
				// TODO(velovix): Load VRAM data
			case scanLineVRAMClocks:
				// We're in mode 0, HBlank period
				vc.setMode(vcMode0)
				// TODO(velovix): Unlock things
				// We're ready to draw the scan line
				vc.drawScanLine(currScanLine)
			}
		} else {
			// We're in mode 1, VBlank period
			vc.setMode(vcMode1)

			if vc.frameTick == scanLineFullClocks*screenHeight {
				// We just finished drawing the frame
				vblankInterruptEnabled := vc.env.mmu.at(ieAddr)&0x01 == 0x01
				if vc.env.interruptsEnabled && vblankInterruptEnabled {
					vc.env.mmu.set(ifAddr, vc.env.mmu.at(ifAddr)|0x01)
				}
				vc.renderer.Present()

				vc.frameCnt++
				if time.Since(vc.lastSecond) >= time.Second {
					fmt.Println("FPS:", vc.frameCnt)
					vc.frameCnt = 0
					vc.lastSecond = time.Now()
				}
			}
		}

		// Should this be before or after operations
		vc.frameTick++
		if vc.frameTick == fullFrameClocks {
			vc.frameTick = 0
		}

	}
}

// drawScanLine draws a scan line at the given height position.
func (vc *videoController) drawScanLine(line int) {
	for x := 0; x < screenWidth; x++ {
		tileID, tileX, tileY := vc.bgTileAt(x, line)

		if _, ok := vc.tiles[tileID]; !ok {
			panic(fmt.Sprintf("invalid tile ID %v", tileID))
		}

		tileData := vc.tiles[tileID]
		dotCode := tileData[(tileY*bgTileHeight)+tileX]
		color := vc.bgPalette[dotCode]

		vc.renderer.SetDrawColor(color.r, color.g, color.b, color.a)
		vc.renderer.DrawPoint(int32(x), int32(line))
	}
}

// bgTileAt finds the ID of background tile at the given screen coordinates and
// the coordinates' offset from the top left of the tile.
func (vc *videoController) bgTileAt(x, y int) (tileID uint8, tileX, tileY int) {
	// Get the coordinates relative to the background and wrap them if
	// necessary
	bgX := x + int(vc.scrollX)
	if bgX < 0 {
		bgX += bgWidth
	} else if bgX > bgWidth {
		bgX -= bgWidth
	}
	bgY := y + int(vc.scrollY)
	if bgY < 0 {
		bgY += bgHeight
	} else if bgY > bgHeight {
		bgY -= bgHeight
	}

	tileOffset := ((bgY/bgTileHeight)*bgWidthInTiles + (bgX / bgTileWidth))
	tileAddr := vc.lcdc.bgTileMapAddr + uint16(tileOffset)

	return vc.env.mmu.at(tileAddr), bgX % bgTileWidth, bgY % bgTileHeight
}

func (vc *videoController) loadBGTiles() map[uint8][]uint8 {
	tileMap := make(map[uint8][]uint8)

	for i := 0; i < 256; i++ {
		tileMap[uint8(i)] = vc.loadTile(uint8(i))
	}

	return tileMap
}

// loadTile loads a single window or background tile and returns the data as a
// slice of dot codes.
func (vc *videoController) loadTile(tile uint8) []uint8 {
	// Find the address of the tile data
	var tileDataAddr uint16
	switch vc.lcdc.windowBGTileDataTableAddr {
	case tileDataTable0:
		// Tile indexes at this data table are signed from -128 to 127
		tileDataAddr = uint16(tileDataTable0 + int(asSigned(tile))*tileBytes)
	case tileDataTable1:
		tileDataAddr = tileDataTable1 + (uint16(tile) * tileBytes)
	default:
		panic(fmt.Sprintf("unknown tile data table %#x", tileDataAddr))
	}

	tileData := make([]uint8, bgTileWidth*bgTileHeight)

	for y := uint16(0); y < bgTileHeight; y++ {
		lower := vc.env.mmu.at(tileDataAddr + (y * 2))
		upper := vc.env.mmu.at(tileDataAddr + ((y * 2) + 1))

		for x := uint16(0); x < bgTileWidth; x++ {
			// Tiles use a weird format. For each row in a tile, there are two
			// bytes. To come up with a single pixel, one bit from each byte is
			// combined into a new two-bit number which selects the color.
			lowerBit := (lower & 0x80) >> 7
			upperBit := (upper & 0x80) >> 7
			paletteID := (upperBit << 1) | lowerBit

			tileData[(y*bgTileHeight)+x] = paletteID

			lower <<= 1
			upper <<= 1
		}
	}

	return tileData
}

func (vc *videoController) destroy() {
	vc.renderer.Destroy()
	vc.window.Destroy()
}

// setMode updates the necessary registers to show what mode the video
// controller is in.
func (vc *videoController) setMode(mode vcMode) {
	statReg := vc.env.mmu.pointerTo(statAddr)

	// Clear the current mode value
	*statReg &= 0xFC
	// Set the mode
	*statReg |= uint8(mode)
}

const (
	_ uint16 = 0x0000

	// tileBytes represents the size of tile data in bytes.
	tileBytes = 16
	// bgTileWidth is the width in pixels of a background tile.
	bgTileWidth = 8
	// bgTileHeight is the height in pixels of a background tile.
	bgTileHeight = 8

	// bgWidthInTiles is the number of tiles per row in the background.
	bgWidthInTiles = 32
	// bgHeightInTiles is the number of tiles per column in the background.
	bgHeightInTiles = 32

	// bgWidth is the width of the background plane.
	bgWidth = bgWidthInTiles * bgTileWidth
	// bgHeight is the height of the background plane.
	bgHeight = 32 * bgTileHeight
)

type vcMode uint8

const (
	// The HBlank period mode.
	vcMode0 vcMode = 0
	// The VBlank period mode.
	vcMode1
	// The OAM RAM loading mode. OAM RAM may not be written to at this time.
	vcMode2
	// The VRAM and OAM RAM loading mode. VRAM and OAM RAM may not be written
	// to at this time.
	vcMode3
)

type spriteSize string

const (
	spriteSize8x8  = "8x8"
	spriteSize8x16 = "8x16"
)

// lcdcConfig contains display configuration information as configured by the
// LCDC memory register.
type lcdcConfig struct {
	// lcdOn controls whether or not the LCD is operational.
	lcdOn bool
	// windowTileMapAddr is the address of the tile map for the window.
	windowTileMapAddr uint16
	// windowOn controls whether or not the window is being displayed.
	windowOn bool
	// windowBGTileDataTableAddr controls what tile data table should be
	// consulted for the window and the background. These two always share the
	// same data table.
	windowBGTileDataTableAddr uint16
	// bgTileMapAddr is the address of the tile map for the background.
	bgTileMapAddr uint16
	// spriteSize controls what size of sprites we're currently using.
	spriteSize spriteSize
	// spritesOn controls whether or not sprites are being displayed.
	spritesOn bool
	// windowBGOn controls whether or not the window and background is being
	// displayed.
	windowBGOn bool
}

// loadLCDC inspects the LCDC register value for display configuration
// information.
func (vc *videoController) loadLCDC() lcdcConfig {
	var config lcdcConfig
	lcdc := vc.env.mmu.at(lcdcAddr)

	config.lcdOn = lcdc&0x80 == 0x80
	if lcdc&0x40 == 0x40 {
		config.windowTileMapAddr = tileMap1
	} else {
		config.windowTileMapAddr = tileMap0
	}
	config.windowOn = lcdc&0x20 == 0x20
	if lcdc&0x10 == 0x10 {
		config.windowBGTileDataTableAddr = tileDataTable1
	} else {
		config.windowBGTileDataTableAddr = tileDataTable0
	}
	if lcdc&0x08 == 0x08 {
		config.bgTileMapAddr = tileMap1
	} else {
		config.bgTileMapAddr = tileMap0
	}
	if lcdc&0x04 == 0x04 {
		config.spriteSize = spriteSize8x16
	} else {
		config.spriteSize = spriteSize8x8
	}
	config.spritesOn = lcdc&0x02 == 0x02
	config.windowBGOn = lcdc&0x01 == 0x01

	return config
}

// statConfig configures LCD configuration information as configured by the
// STAT memory register.
type statConfig struct {
	// lyEqualsLYCInterruptOn is true if an interrupt should be generated when
	// the LY and LYC memory registers are equal.
	lyEqualsLYCInterruptOn bool
	// mode2InterruptOn is true if an interrupt should be generated when the
	// video controller switches to mode 2.
	mode2InterruptOn bool
	// mode1InterruptOn is true if an interrupt should be generated when the
	// video controller switches to mode 1.
	mode1InterruptOn bool
	// mode0InterruptOn is true if an interrupt should be generated when the
	// video controller switches to mode 0.
	mode0InterruptOn bool
	// lyEqualsLYC is true if the LY and LYC memory registers are equal.
	lyEqualsLYC bool
	// vcMode is the current mode of the video controller.
	mode vcMode
}

// loadSTAT inspects the STAT register value for LCD configuration information.
func (vc *videoController) loadSTAT() statConfig {
	var config statConfig
	stat := vc.env.mmu.at(statAddr)

	config.lyEqualsLYCInterruptOn = stat&0x40 == 0x40
	config.mode2InterruptOn = stat&0x20 == 0x20
	config.mode1InterruptOn = stat&0x10 == 0x10
	config.mode0InterruptOn = stat&0x08 == 0x08
	config.lyEqualsLYC = stat&0x04 == 0x04
	config.mode = vcMode(stat & 0x03)

	return config
}

// saveSTAT saves the given STAT configuration into the memory register.
func (vc *videoController) saveSTAT(config statConfig) {
	stat := vc.env.mmu.pointerTo(statAddr)

	if config.lyEqualsLYCInterruptOn {
		*stat |= 0x40
	} else {
		*stat &= ^uint8(0x40)
	}
	if config.mode2InterruptOn {
		*stat |= 0x20
	} else {
		*stat &= ^uint8(0x20)
	}
	if config.mode1InterruptOn {
		*stat |= 0x10
	} else {
		*stat &= ^uint8(0x10)
	}
	if config.mode0InterruptOn {
		*stat |= 0x08
	} else {
		*stat &= ^uint8(0x08)
	}
	if config.lyEqualsLYC {
		*stat |= 0x04
	} else {
		*stat &= ^uint8(0x04)
	}
	// Clear and set the mode
	*stat &= 0xFC
	*stat |= uint8(config.mode)
}

// loadBGPalette inspects the BGP register value and returns a map that maps
// dot data to actual colors.
func (vc *videoController) loadBGPalette() map[uint8]color {
	bgp := vc.env.mmu.at(bgpAddr)
	palette := make(map[uint8]color)

	for dotData := uint8(0); dotData <= 0x3; dotData++ {
		colorType := (bgp & 0xC0) >> 6

		var c color
		switch colorType {
		case 0x00:
			c = color{0, 0, 0, 255}
		case 0x01:
			c = color{98, 78, 80, 255}
		case 0x02:
			c = color{219, 179, 180, 255}
		case 0x03:
			c = color{255, 255, 255, 255}
		}

		palette[dotData] = c
		bgp <<= 2
	}

	return palette
}

type color struct {
	r, g, b, a uint8
}
