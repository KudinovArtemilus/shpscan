package main

// profile.go — что именно считать для конкретной машины.
// Здесь нет логики, только списки сигналов. Чтобы добавить новую машину,
// достаточно описать ещё один Profile и дописать его в список Profiles.

type ChartGroup struct {
	Title   string
	Signals []string
}

type Profile struct {
	Name          string
	Detect        string      // сигнал, по которому машина узнаётся: есть в файле — значит, этот профиль
	StepSignal    string      // номер шага цикла (если у машины он есть)
	LoadSignal    string      // по нему определяется окно «под нагрузкой»
	Forces        []string    // максимум за цикл и среднее под нагрузкой
	LeftRight     [][3]string // {название, левый сигнал, правый сигнал} — перекос
	Peaks         []string    // максимум за цикл
	Mins          []string    // минимум за цикл
	Ranges        []string    // размах (макс − мин) за цикл
	AbsPeaks      []string    // максимум модуля (перекосы, ошибки регулирования)
	AtLoadEnd     []string    // значение в конце нагрузки
	AtLoadPeak    []string    // значение в момент максимального усилия
	Counters      []string    // счётчики, берём последнее значение
	IDSignals     [3]string   // год, порядковый номер, плавка
	FaultPrefixes []string    // биты с такими префиксами считаем авариями
	PairSuffixes  [][2]string // {окончание задания, окончание обратной связи} — слежение клапанов и насосов
	Tiles         []string    // показатели для плиток в шапке отчёта
	Charts        []ChartGroup
}

// KPS — колёсопрокатный стан (файлы *_OP3_*.shp).
var KPS = Profile{
	Name:       "КПС",
	Detect:     "MRF_Force_F",
	StepSignal: "ActualStep",
	LoadSignal: "MRF_Force_F",
	Forces: []string{
		"MRF_Force_F", "WRFLeftForce_F", "WRFRightForce_F",
		"SRFLeftForce_F", "SRFRightForce_F", "LCRF_Force_F", "UCRF_Force_F",
		"GRFLeftForce_F", "GRFRightForce_F",
	},
	LeftRight: [][3]string{
		{"WRF_Force", "WRFLeftForce_F", "WRFRightForce_F"},
		{"SRF_Force", "SRFLeftForce_F", "SRFRightForce_F"},
		{"WRD_Torque", "WRDLeftTorque_%", "WRDRightTorque_%"},
	},
	Peaks: []string{
		"WRDLeftTorque_%", "WRDRightTorque_%", "MRD_Rev_n",
		"SRDLeftRev_n", "SRDRightRev_n",
		"PumpPrP1_1_p", "PumpPrP1_2_p", "PumpPrP2_1_p",
		"FormRevs", "S7 Actual Cycle Time (ms)",
	},
	Mins:      []string{"HydAccuPressure_p", "PumpPrP2_1_p"},
	Ranges:    []string{"HydAccuPressure_p"},
	AtLoadEnd: []string{"Diameter_CR", "lvWheelDmrLaser", "D1i", "D1a", "MRF_RelPos"},
	Counters: []string{
		"MR_StokesSinceChange", "WRL_StokesSinceChange", "WRR_StokesSinceChange",
		"SRL_StokesSinceChange", "SRR_StokesSinceChange",
		"UCR_StokesSinceChange", "LCR_StokesSinceChange",
		"GRL_StokesSinceChange", "GRR_StokesSinceChange",
	},
	IDSignals:     [3]string{"Year", "ConsecutiveNo", "HeatID"},
	FaultPrefixes: []string{"Fehler", "fi.", "fi_", "Fi"},
	PairSuffixes:  [][2]string{{"_U", "_FU"}, {"_U", "_F_U"}},
	Tiles:         []string{"MRF_Force_F.max", "WRFLeftForce_F.max", "WRFRightForce_F.max", "HydAccuPressure_p.min", "Diameter_CR.end"},
	Charts: []ChartGroup{
		{"Усилия", []string{"MRF_Force_F", "WRFLeftForce_F", "WRFRightForce_F", "SRFLeftForce_F", "SRFRightForce_F", "LCRF_Force_F", "UCRF_Force_F"}},
		{"Приводы: обороты и момент", []string{"MRD_Rev_n", "SRDLeftRev_n", "SRDRightRev_n", "WRDLeftRev_n", "WRDLeftTorque_%", "WRDRightTorque_%"}},
		{"Гидравлика: давления", []string{"PumpPrP1_1_p", "PumpPrP1_2_p", "PumpPrP2_1_p", "HydAccuPressure_p"}},
		{"Диаметр колеса", []string{"Diameter_CR", "lvWheelDmrLaser", "D1i", "D1a"}},
		{"Положения подач", []string{"MRF_RelPos", "WRFLeftRelPos", "WRFRightRelPos", "SRFLeftRelPos", "SRFRightRelPos"}},
	},
}

// M10 — пресс М10 (файлы *_OP1_*.shp и *_OP2_*.shp, одна и та же таблица сигналов).
var M10 = Profile{
	Name:       "Пресс М10",
	Detect:     "slide:total force [kN]",
	LoadSignal: "slide:total force [kN]",
	Forces:     []string{"slide:total force [kN]", "SCu:netto force [kN]", "slide: return force [kN]"},
	Peaks: []string{
		"slide: speed [mm/s]", "slide ejector speed [mm/s]",
		"slide:head side pressure [bar]", "slide:ring side pressure [bar]",
		"Pump 1.1 actPressure", "Pump 2.1 actPressure", "Pump 3.1 actPressure",
		"Pump 4.1 actPressure", "Pump 5.1 actPressure", "Pump 6.1 actPressure",
		"hyd:high pressure [bar]",
		"hyd:temp bottom tank =03+44M0-B", "hyd:temp upper tank =03+93M0-B1",
		// давление утечек в корпус насоса растёт по мере его износа
		"Oildr: MD1 Casedrain Pressure", "Oildr: MD2 Casedrain Pressure", "Oildr: MD3 Casedrain Pressure",
		"Oildr: MD4 Casedrain Pressure", "Oildr: MD6 Casedrain Pressure",
	},
	Mins: []string{
		"hyd:high pressure [bar]", "hyd:low pressure [bar]",
		"pressure accu ring valve1 [bar]", "pressure accu ring valve3 [bar]",
		"slide:relative position [mm]", "hyd:level bottom tank =03+44M0-",
	},
	Ranges: []string{"hyd:high pressure [bar]", "pressure accu ring valve1 [bar]", "pressure accu ring valve3 [bar]"},
	AbsPeaks: []string{
		"slide:deviation left-right [mm]",
		"CCyl Left lvPosCtrlDev", "CCyl Right lvPosCtrlDev",
		"LCyl Left lvPosCtrlDev", "LCyl Right lvPosCtrlDev",
	},
	AtLoadPeak:    []string{"slide:relative position [mm]", "slide: speed [mm/s]"},
	Counters:      []string{"Die.StokesSinceChange", "Punsh.StokesSinceChange"},
	IDSignals:     [3]string{"PartTracking_PieceId_Year", "PartTracking_PieceId_Consecutiv", "PartTracking_PieceId_HeatID"},
	FaultPrefixes: []string{`"fi"`},
	PairSuffixes: [][2]string{
		{" set value [V]", " piston pos.[V]"}, {" set value [V]", " pistion pos.[V]"},
		{": set value [V]", ":piston pos.[V]"},
		{" U", " PinF"}, {" svValve", " avValve"}, {" setPivoting", " actPivoting"},
		{"SetVal", "ActVal"},
	},
	Tiles: []string{"slide:total force [kN].max", "slide: speed [mm/s].max", "slide:deviation left-right [mm].absmax", "hyd:high pressure [bar].min"},
	Charts: []ChartGroup{
		{"Ползун: усилие", []string{"slide:total force [kN]", "SCu:netto force [kN]", "slide: return force [kN]"}},
		{"Ползун: положение и скорость", []string{"slide:relative position [mm]", "slide: speed [mm/s]"}},
		{"Ползун: давления", []string{"slide:head side pressure [bar]", "slide:ring side pressure [bar]", "slide:pressure adj.force[bar]", "pressure accu ring valve1 [bar]", "pressure accu ring valve3 [bar]"}},
		{"Насосы: давление", []string{"Pump 1.1 actPressure", "Pump 2.1 actPressure", "Pump 3.1 actPressure", "Pump 4.1 actPressure", "Pump 5.1 actPressure", "Pump 6.1 actPressure"}},
		{"Гидростанция", []string{"hyd:high pressure [bar]", "hyd:low pressure [bar]"}},
		{"Цилиндры CCyl и LCyl", []string{"CCyl Left Encoder", "CCyl Right Encoder", "LCyl Left Encoder", "LCyl Right Encoder"}},
		{"Стол и выталкиватель", []string{"MB ActPos", "slide ejector encoder [mm]"}},
	},
}

// P5 — пресс 5. Сигналы похожи на М10, но названы иначе, насосов три, номера заготовки в файле нет.
var P5 = Profile{
	Name:       "Пресс 5",
	Detect:     "slide total force [kN]",
	LoadSignal: "slide total force [kN]",
	Forces:     []string{"slide total force [kN]", `SCu":actual force[kN]`, "slide return force [kN]", "BEj:head side actual force[kN]"},
	Peaks: []string{
		"slide: speed [mm/s]", "SCu:actual speed[mm/s]", "BEj:actual speed[mm/s]",
		"slide:head side pressure 12B002", "slide:ring side pressure 12B013",
		"Pump 1.1 actPressure", "Pump 2.1 actPressure", "Pump 3.1 actPressure",
		"hyd:03-B135.1 hp [bar]",
		"Oildr: MD1 Casedrain Pressure", "Oildr: MD2 Casedrain Pressure", "Oildr: MD3 Casedrain Pressure",
	},
	Mins: []string{
		"hyd:03-B135.1 hp [bar]", "hyd:03-B235 lp [bar]",
		"slide: RV2 accu pressure[bar]", "SCu:accu pressure[bar]",
		"slide:relative position [mm]",
	},
	Ranges:        []string{"hyd:03-B135.1 hp [bar]", "slide: RV2 accu pressure[bar]", "SCu:accu pressure[bar]"},
	AbsPeaks:      []string{"slide:deviation left-right [mm]"},
	AtLoadPeak:    []string{"slide:relative position [mm]", "slide: speed [mm/s]"},
	FaultPrefixes: []string{`"SPlc".Error`},
	PairSuffixes: [][2]string{
		// имена в файле обрезаны регистратором, поэтому окончания такие странные
		{" SetVal_adjust pres", " PinF[V]"}, {" SetVal_adjust pressu", " PinF[V]"},
		{" ring valve SetVa", " PinF[U]"},
		{":set value [1/min]", ":act value [1/min]"},
		{" svValve", " avValve"}, {" setPivoting", " actPivoting"},
	},
	Tiles: []string{"slide total force [kN].max", "slide: speed [mm/s].max", "slide:deviation left-right [mm].absmax", "hyd:03-B135.1 hp [bar].min"},
	Charts: []ChartGroup{
		{"Ползун: усилие", []string{"slide total force [kN]", `SCu":actual force[kN]`, "slide return force [kN]"}},
		{"Ползун: положение и скорость", []string{"slide:relative position [mm]", "slide: speed [mm/s]"}},
		{"Ползун: давления", []string{"slide:head side pressure 12B002", "slide:ring side pressure 12B013", "slide:12-B102 pressure adjust [", "slide: RV2 accu pressure[bar]"}},
		{"Насосы: давление", []string{"Pump 1.1 actPressure", "Pump 2.1 actPressure", "Pump 3.1 actPressure"}},
		{"Гидростанция", []string{"hyd:03-B135.1 hp [bar]", "hyd:03-B235 lp [bar]"}},
		{"Подушка и выталкиватель: положение", []string{"SCu:actual position[mm]", "BEj:actual position[mm]"}},
	},
}

// Unknown — файл читается, но машина не опознана: считаем только длительность.
var Unknown = Profile{Name: "Неизвестная машина"}

var Profiles = []*Profile{&KPS, &M10, &P5}

// pickProfile выбирает профиль по сигналам, которые есть в файле.
func pickProfile(f *File) *Profile {
	for _, p := range Profiles {
		if _, ok := f.anIdx[p.Detect]; ok {
			return p
		}
	}
	return &Unknown
}
