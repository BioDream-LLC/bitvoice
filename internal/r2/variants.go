package r2

import "sort"

// Variant is a country's MFC-R2: which signal (1 to 15; 0 for none) means what. Group A and B are
// backward signals, Group C Mexico's third backward group; Group I and II are forward.
type Variant struct {
	Name   string
	CDBits int // the C and D bits, fixed: 01 (ITU) or 11 (China)
	// GetANIFirst: the backward side asks for the caller's category and number after the first
	// digit of the number dialled (most variants), else after the whole number. ANICanComeFirst: the
	// caller's number may come before the number dialled (China); ForceMaxANI: ask for exactly MaxANI
	// digits of it, there being no end signal.
	GetANIFirst     bool
	ANICanComeFirst bool
	ForceMaxANI     bool

	ANextDNIS, ADNISMinus1, ADNISMinus2, ADNISMinus3, AAllDNISAgain int
	ANextANI, ACategory, ACategoryAndGC, AChangeToG2                int
	AAddressCompleteCharge, ACongestion                             int

	BAcceptCharge, BAcceptNoCharge, BBusy, BCongestion, BUnallocated, BOutOfOrder, BSIT, BNumberChanged int

	CNextANI, CChangeToG2, CNextDNISChangeToGA, CCongestion int

	INoMoreDNIS, INoMoreANI, IANIRestricted int

	IINational, IINationalPriority, IITest, IIInternational, IIInternationalPriority, IICollect, IIPayPhone int

	// AUnallocated is a Group A "vacant number" (China's A-5).
	AUnallocated int

	// China No. 1: the category request (A-6) is answered with KA, a Group I signal of the caller's
	// class (KAOrdinary, KAPriority), and the change to Group B (A-3) with KD, the Group II call type.
	// What a Group B signal means depends on the KD: China says so.
	China                  bool
	KAOrdinary, KAPriority int
}

// China No. 1's KD (Group II, the call type) and KB (Group B) signals, from ITU-T Q.441 as China
// applies it (Cisco VCO/4K China 3 Supplement, tables 2-2 and 2-4).
const (
	kdTollOperator = 1
	kdTollAuto     = 2
	kdLocal        = 3
	kdLocalPrio    = 4
	kdTest         = 6

	kbIdle         = 1 // every KD
	kbBusyLocal    = 2 // KD 1, 2, 6: busy, local duty
	kbBusyToll     = 3 // KD 1, 2, 6: busy, toll duty
	kbCongestion   = 4 // KD 1, 2, 6: congestion; KD 3, 4: called subscriber busy or congestion
	kbVacant       = 5 // every KD
	kbIdleCallerCt = 6 // KD 3, 4: called subscriber idle, calling party control
)

// localKD says whether a KD is a local call (3 or 4), whose Group B signals read differently.
func localKD(kd int) bool { return kd == kdLocal || kd == kdLocalPrio }

// itu is ITU-T Q.441 as most countries use it.
func itu() Variant {
	return Variant{Name: "itu", CDBits: 0x1, GetANIFirst: true,
		ANextDNIS: 1, ADNISMinus1: 2, ADNISMinus2: 7, ADNISMinus3: 8, ANextANI: 5, ACategory: 5, AChangeToG2: 3,
		AAddressCompleteCharge: 6, ACongestion: 4,
		BAcceptCharge: 6, BAcceptNoCharge: 7, BBusy: 3, BCongestion: 4, BUnallocated: 5, BOutOfOrder: 8, BSIT: 2,
		INoMoreDNIS: 15, INoMoreANI: 15, IANIRestricted: 12,
		IINational: 1, IINationalPriority: 2, IITest: 3, IIInternational: 7, IIInternationalPriority: 9}
}

// Variants are the countries BitVoice knows, by code.
var Variants = map[string]*Variant{}

func init() {
	add := func(code string, f func(v *Variant)) {
		v := itu()
		v.Name = code
		if f != nil {
			f(&v)
		}
		Variants[code] = &v
	}
	add("itu", nil)
	add("cz", nil)               // Czech Republic
	add("ec", nil)               // Ecuador
	add("ph", nil)               // Philippines
	add("ar", func(v *Variant) { // Argentina
		v.INoMoreDNIS, v.IANIRestricted, v.INoMoreANI = 0, 15, 12
		v.IIPayPhone = 4
	})
	add("br", func(v *Variant) { // Brazil
		v.INoMoreDNIS, v.IANIRestricted = 0, 12
		v.IICollect = 8
		v.AAddressCompleteCharge, v.ADNISMinus1, v.AAllDNISAgain = 0, 9, 2
		v.BAcceptCharge, v.BBusy, v.BAcceptNoCharge, v.BSIT, v.BNumberChanged, v.BUnallocated = 1, 2, 5, 6, 3, 7
	})
	// China No. 1 (Cisco VCO/4K China 3 Supplement, chapters 2 and 4): the number dialled first, each
	// digit asked by A-1 and ended by I-15; A-6 asks for KA and the caller's number (each digit by A-1,
	// ended by I-15); A-3 for KD; then KB. A-2 starts the number again; C and D bits both 1.
	add("cn", func(v *Variant) { // China
		v.CDBits, v.GetANIFirst, v.China = 0x3, false, true
		v.ANextDNIS, v.ADNISMinus1, v.ADNISMinus2, v.ADNISMinus3, v.AAllDNISAgain = 1, 0, 0, 0, 2
		v.ACategory, v.ANextANI, v.AChangeToG2, v.ACongestion, v.AUnallocated, v.AAddressCompleteCharge = 6, 1, 3, 4, 5, 0
		v.INoMoreDNIS, v.INoMoreANI, v.IANIRestricted = 15, 15, 0
		v.KAOrdinary, v.KAPriority = 1, 8
		v.IINational, v.IINationalPriority, v.IITest, v.IIInternational, v.IIInternationalPriority = kdLocal, kdLocalPrio, kdTest, kdTollAuto, 0
		v.BAcceptCharge, v.BAcceptNoCharge, v.BBusy, v.BCongestion, v.BUnallocated, v.BOutOfOrder, v.BSIT = kbIdle, 0, kbBusyLocal, kbCongestion, kbVacant, 0, 0
	})
	add("co", func(v *Variant) { // Colombia
		v.IANIRestricted = 12
		v.BAcceptCharge, v.BBusy, v.BAcceptNoCharge, v.BUnallocated = 1, 2, 5, 6
	})
	add("mx", func(v *Variant) { // Mexico: the category request switches to Group C
		v.GetANIFirst = false
		v.ACategory, v.ACategoryAndGC, v.AAddressCompleteCharge, v.ANextANI = 0, 6, 0, 0
		v.BAcceptCharge, v.BAcceptNoCharge, v.BBusy, v.BUnallocated, v.BSIT = 1, 5, 2, 2, 0
		v.CNextANI, v.CChangeToG2, v.CNextDNISChangeToGA, v.CCongestion = 1, 3, 5, 4
		v.INoMoreDNIS, v.IANIRestricted = 0, 15
	})
	add("ve", func(v *Variant) { // Venezuela
		v.ANextANI = 9
		v.IANIRestricted, v.INoMoreDNIS = 12, 0
	})
}

// Codes lists the variants' codes, sorted.
func Codes() []string {
	var out []string
	for k := range Variants {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (v *Variant) categoryTone(c int) int {
	t := map[int]int{CategoryNational: v.IINational, CategoryNationalPriority: v.IINationalPriority,
		CategoryInternational: v.IIInternational, CategoryInternationalPriority: v.IIInternationalPriority,
		CategoryCollect: v.IICollect, CategoryTest: v.IITest, CategoryPayPhone: v.IIPayPhone}[c]
	if t == 0 {
		t = v.IINational
	}
	return t
}

func (v *Variant) categoryOf(tone int) int {
	switch tone {
	case v.IINational:
		return CategoryNational
	case v.IINationalPriority:
		return CategoryNationalPriority
	case v.IIInternational:
		return CategoryInternational
	case v.IIInternationalPriority:
		return CategoryInternationalPriority
	case v.IITest:
		return CategoryTest
	}
	if tone != 0 && tone == v.IICollect {
		return CategoryCollect
	}
	if tone != 0 && tone == v.IIPayPhone {
		return CategoryPayPhone
	}
	return CategoryNational
}
