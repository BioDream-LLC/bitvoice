package ilbc

// The reference's iLBC_define.h.
const (
	fs              = float32(8000.0)
	blocklMax       = 240
	nsubMax         = 6
	nasubMax        = 4
	subl            = 40
	stateLen        = 80
	stateShortLen30 = 58
	stateShortLen20 = 57

	lpcFilterorder      = 10
	lpcChirpSyntdenum   = float32(0.9025)
	lpcChirpWeightdenum = float32(0.4222)
	lpcLookback         = 60
	lpcNMax             = 2
	lpcAsymdiff         = 20
	lpcBW               = float32(60.0)
	lpcWN               = float32(1.0001)
	lsfNsplit           = 3
	lsfNumberOfSteps    = 4
	lpcHalforder        = lpcFilterorder / 2

	cbNStages       = 3
	cbExpand        = 2
	cbMeml          = 147
	cbFilterlen     = 2 * 4
	cbHalffilterlen = 4
	cbResrange      = 34
	cbMaxgain       = float32(1.3)

	enhBlockl       = 80
	enhBlocklHalf   = enhBlockl / 2
	enhHL           = 3
	enhSlop         = 2
	enhPlocsl       = 20
	enhOverhang     = 2
	enhUps0         = 4
	enhFL0          = 3
	enhVectl        = enhBlockl + 2*enhFL0
	enhCorrdim      = 2*enhSlop + 1
	enhNblocks      = blocklMax / enhBlockl
	enhNblocksExtra = 5
	enhNblocksTot   = 8
	enhBufl         = enhNblocksTot * enhBlockl
	enhAlpha0       = float32(0.05)

	filterorderDS = 7
	delayDS       = 3
	factorDS      = 2

	noOfBytes20 = 38
	noOfBytes30 = 50
	noOfWords20 = 19
	noOfWords30 = 25

	stateBits  = 3
	byteLen    = 8
	ulpClasses = 3

	floatMax  = float32(1.0e37)
	eps       = float32(2.220446049250313e-016)
	pi        = float32(3.14159265358979323846)
	minSample = -32768
	maxSample = 32767
	twoPi     = float32(6.283185307)
	pi2       = float32(0.159154943)
)

// encInst is the encoder's state (iLBC_Enc_Inst_t).
type encInst struct {
	mode          int
	blockl        int
	nsub          int
	nasub         int
	noOfBytes     int
	noOfWords     int
	lpcN          int
	stateShortLen int
	ulpInst       *ulpInst
	anaMem        [lpcFilterorder]float32
	lsfold        [lpcFilterorder]float32
	lsfdeqold     [lpcFilterorder]float32
	lpcBuffer     [lpcLookback + blocklMax]float32
	hpimem        [4]float32
}

// decInst is the decoder's state (iLBC_Dec_Inst_t).
type decInst struct {
	mode          int
	blockl        int
	nsub          int
	nasub         int
	noOfBytes     int
	noOfWords     int
	lpcN          int
	stateShortLen int
	ulpInst       *ulpInst
	syntMem       [lpcFilterorder]float32
	lsfdeqold     [lpcFilterorder]float32
	lastLag       int
	prevLag       int
	consPLICount  int
	prevPLI       int
	prevEnhPl     int
	prevLpc       [lpcFilterorder + 1]float32
	prevResidual  [nsubMax * subl]float32
	per           float32
	seed          uint32
	oldSyntdenum  [(lpcFilterorder + 1) * nsubMax]float32
	hpomem        [4]float32
	useEnhancer   int
	enhBuf        [enhBufl]float32
	enhPeriod     [enhNblocksTot]float32
}
