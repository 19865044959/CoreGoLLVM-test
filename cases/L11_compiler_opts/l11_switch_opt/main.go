// L11: 大型 switch（320 个 case）、64 段 if/else 链与 64 分支字符串 switch
// 必须与独立参考函数逐项一致；结果在 GC 后稳定。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 1
	}
	if n > 100 {
		n = 100
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

// classifySwitch 有 320 个显式 case，每个返回值由固定公式生成。
func classifySwitch(x int) int {
	switch x {
	case 0:
		return 5
	case 1:
		return 25
	case 2:
		return 59
	case 3:
		return 107
	case 4:
		return 169
	case 5:
		return 245
	case 6:
		return 335
	case 7:
		return 439
	case 8:
		return 557
	case 9:
		return 689
	case 10:
		return 835
	case 11:
		return 995
	case 12:
		return 160
	case 13:
		return 348
	case 14:
		return 550
	case 15:
		return 766
	case 16:
		return 996
	case 17:
		return 231
	case 18:
		return 489
	case 19:
		return 761
	case 20:
		return 38
	case 21:
		return 338
	case 22:
		return 652
	case 23:
		return 980
	case 24:
		return 313
	case 25:
		return 669
	case 26:
		return 30
	case 27:
		return 414
	case 28:
		return 812
	case 29:
		return 215
	case 30:
		return 641
	case 31:
		return 72
	case 32:
		return 526
	case 33:
		return 994
	case 34:
		return 467
	case 35:
		return 963
	case 36:
		return 464
	case 37:
		return 988
	case 38:
		return 517
	case 39:
		return 60
	case 40:
		return 626
	case 41:
		return 197
	case 42:
		return 791
	case 43:
		return 390
	case 44:
		return 3
	case 45:
		return 639
	case 46:
		return 280
	case 47:
		return 944
	case 48:
		return 613
	case 49:
		return 296
	case 50:
		return 1002
	case 51:
		return 713
	case 52:
		return 438
	case 53:
		return 177
	case 54:
		return 939
	case 55:
		return 706
	case 56:
		return 487
	case 57:
		return 282
	case 58:
		return 91
	case 59:
		return 923
	case 60:
		return 760
	case 61:
		return 611
	case 62:
		return 476
	case 63:
		return 355
	case 64:
		return 248
	case 65:
		return 155
	case 66:
		return 76
	case 67:
		return 11
	case 68:
		return 969
	case 69:
		return 932
	case 70:
		return 909
	case 71:
		return 900
	case 72:
		return 905
	case 73:
		return 924
	case 74:
		return 957
	case 75:
		return 1004
	case 76:
		return 56
	case 77:
		return 131
	case 78:
		return 220
	case 79:
		return 323
	case 80:
		return 440
	case 81:
		return 571
	case 82:
		return 716
	case 83:
		return 875
	case 84:
		return 39
	case 85:
		return 226
	case 86:
		return 427
	case 87:
		return 642
	case 88:
		return 871
	case 89:
		return 105
	case 90:
		return 362
	case 91:
		return 633
	case 92:
		return 918
	case 93:
		return 208
	case 94:
		return 521
	case 95:
		return 848
	case 96:
		return 180
	case 97:
		return 535
	case 98:
		return 904
	case 99:
		return 278
	case 100:
		return 675
	case 101:
		return 77
	case 102:
		return 502
	case 103:
		return 941
	case 104:
		return 385
	case 105:
		return 852
	case 106:
		return 324
	case 107:
		return 819
	case 108:
		return 319
	case 109:
		return 842
	case 110:
		return 370
	case 111:
		return 921
	case 112:
		return 477
	case 113:
		return 47
	case 114:
		return 640
	case 115:
		return 238
	case 116:
		return 859
	case 117:
		return 485
	case 118:
		return 125
	case 119:
		return 788
	case 120:
		return 456
	case 121:
		return 138
	case 122:
		return 843
	case 123:
		return 553
	case 124:
		return 277
	case 125:
		return 15
	case 126:
		return 776
	case 127:
		return 542
	case 128:
		return 322
	case 129:
		return 116
	case 130:
		return 933
	case 131:
		return 755
	case 132:
		return 591
	case 133:
		return 441
	case 134:
		return 305
	case 135:
		return 183
	case 136:
		return 75
	case 137:
		return 990
	case 138:
		return 910
	case 139:
		return 844
	case 140:
		return 792
	case 141:
		return 754
	case 142:
		return 730
	case 143:
		return 720
	case 144:
		return 724
	case 145:
		return 742
	case 146:
		return 774
	case 147:
		return 820
	case 148:
		return 880
	case 149:
		return 954
	case 150:
		return 33
	case 151:
		return 135
	case 152:
		return 251
	case 153:
		return 381
	case 154:
		return 525
	case 155:
		return 683
	case 156:
		return 855
	case 157:
		return 32
	case 158:
		return 232
	case 159:
		return 446
	case 160:
		return 674
	case 161:
		return 916
	case 162:
		return 163
	case 163:
		return 433
	case 164:
		return 717
	case 165:
		return 6
	case 166:
		return 318
	case 167:
		return 644
	case 168:
		return 984
	case 169:
		return 329
	case 170:
		return 697
	case 171:
		return 70
	case 172:
		return 466
	case 173:
		return 876
	case 174:
		return 291
	case 175:
		return 729
	case 176:
		return 172
	case 177:
		return 638
	case 178:
		return 109
	case 179:
		return 603
	case 180:
		return 102
	case 181:
		return 624
	case 182:
		return 151
	case 183:
		return 701
	case 184:
		return 256
	case 185:
		return 834
	case 186:
		return 417
	case 187:
		return 14
	case 188:
		return 634
	case 189:
		return 259
	case 190:
		return 907
	case 191:
		return 560
	case 192:
		return 227
	case 193:
		return 917
	case 194:
		return 612
	case 195:
		return 321
	case 196:
		return 44
	case 197:
		return 790
	case 198:
		return 541
	case 199:
		return 306
	case 200:
		return 85
	case 201:
		return 887
	case 202:
		return 694
	case 203:
		return 515
	case 204:
		return 350
	case 205:
		return 199
	case 206:
		return 62
	case 207:
		return 948
	case 208:
		return 839
	case 209:
		return 744
	case 210:
		return 663
	case 211:
		return 596
	case 212:
		return 543
	case 213:
		return 504
	case 214:
		return 479
	case 215:
		return 468
	case 216:
		return 471
	case 217:
		return 488
	case 218:
		return 519
	case 219:
		return 564
	case 220:
		return 623
	case 221:
		return 696
	case 222:
		return 783
	case 223:
		return 884
	case 224:
		return 999
	case 225:
		return 119
	case 226:
		return 262
	case 227:
		return 419
	case 228:
		return 590
	case 229:
		return 775
	case 230:
		return 974
	case 231:
		return 178
	case 232:
		return 405
	case 233:
		return 646
	case 234:
		return 901
	case 235:
		return 161
	case 236:
		return 444
	case 237:
		return 741
	case 238:
		return 43
	case 239:
		return 368
	case 240:
		return 707
	case 241:
		return 51
	case 242:
		return 418
	case 243:
		return 799
	case 244:
		return 185
	case 245:
		return 594
	case 246:
		return 8
	case 247:
		return 445
	case 248:
		return 896
	case 249:
		return 352
	case 250:
		return 831
	case 251:
		return 315
	case 252:
		return 822
	case 253:
		return 334
	case 254:
		return 869
	case 255:
		return 409
	case 256:
		return 972
	case 257:
		return 540
	case 258:
		return 122
	case 259:
		return 727
	case 260:
		return 337
	case 261:
		return 970
	case 262:
		return 608
	case 263:
		return 260
	case 264:
		return 935
	case 265:
		return 615
	case 266:
		return 309
	case 267:
		return 17
	case 268:
		return 748
	case 269:
		return 484
	case 270:
		return 234
	case 271:
		return 1007
	case 272:
		return 785
	case 273:
		return 577
	case 274:
		return 383
	case 275:
		return 203
	case 276:
		return 37
	case 277:
		return 894
	case 278:
		return 756
	case 279:
		return 632
	case 280:
		return 522
	case 281:
		return 426
	case 282:
		return 344
	case 283:
		return 276
	case 284:
		return 222
	case 285:
		return 182
	case 286:
		return 156
	case 287:
		return 144
	case 288:
		return 146
	case 289:
		return 162
	case 290:
		return 192
	case 291:
		return 236
	case 292:
		return 294
	case 293:
		return 366
	case 294:
		return 452
	case 295:
		return 552
	case 296:
		return 666
	case 297:
		return 794
	case 298:
		return 936
	case 299:
		return 83
	case 300:
		return 253
	case 301:
		return 437
	case 302:
		return 635
	case 303:
		return 847
	case 304:
		return 64
	case 305:
		return 304
	case 306:
		return 558
	case 307:
		return 826
	case 308:
		return 99
	case 309:
		return 395
	case 310:
		return 705
	case 311:
		return 20
	case 312:
		return 358
	case 313:
		return 710
	case 314:
		return 67
	case 315:
		return 447
	case 316:
		return 841
	case 317:
		return 240
	case 318:
		return 662
	case 319:
		return 89
	default:
		return -1
	}
}

// classifyIf 是 64 段 if/else 链，每段覆盖 4 个整数。
func classifyIf(x int) int {
	if x < 0 {
		return -1
	}
	if x < 4 {
		return x*1 + 0
	}
	if x < 8 {
		return x*2 + 3
	}
	if x < 12 {
		return x*3 + 6
	}
	if x < 16 {
		return x*4 + 9
	}
	if x < 20 {
		return x*5 + 12
	}
	if x < 24 {
		return x*6 + 15
	}
	if x < 28 {
		return x*7 + 18
	}
	if x < 32 {
		return x*1 + 21
	}
	if x < 36 {
		return x*2 + 24
	}
	if x < 40 {
		return x*3 + 27
	}
	if x < 44 {
		return x*4 + 30
	}
	if x < 48 {
		return x*5 + 33
	}
	if x < 52 {
		return x*6 + 36
	}
	if x < 56 {
		return x*7 + 39
	}
	if x < 60 {
		return x*1 + 42
	}
	if x < 64 {
		return x*2 + 45
	}
	if x < 68 {
		return x*3 + 48
	}
	if x < 72 {
		return x*4 + 51
	}
	if x < 76 {
		return x*5 + 54
	}
	if x < 80 {
		return x*6 + 57
	}
	if x < 84 {
		return x*7 + 60
	}
	if x < 88 {
		return x*1 + 63
	}
	if x < 92 {
		return x*2 + 66
	}
	if x < 96 {
		return x*3 + 69
	}
	if x < 100 {
		return x*4 + 72
	}
	if x < 104 {
		return x*5 + 75
	}
	if x < 108 {
		return x*6 + 78
	}
	if x < 112 {
		return x*7 + 81
	}
	if x < 116 {
		return x*1 + 84
	}
	if x < 120 {
		return x*2 + 87
	}
	if x < 124 {
		return x*3 + 90
	}
	if x < 128 {
		return x*4 + 93
	}
	if x < 132 {
		return x*5 + 96
	}
	if x < 136 {
		return x*6 + 99
	}
	if x < 140 {
		return x*7 + 102
	}
	if x < 144 {
		return x*1 + 105
	}
	if x < 148 {
		return x*2 + 108
	}
	if x < 152 {
		return x*3 + 111
	}
	if x < 156 {
		return x*4 + 114
	}
	if x < 160 {
		return x*5 + 117
	}
	if x < 164 {
		return x*6 + 120
	}
	if x < 168 {
		return x*7 + 123
	}
	if x < 172 {
		return x*1 + 126
	}
	if x < 176 {
		return x*2 + 129
	}
	if x < 180 {
		return x*3 + 132
	}
	if x < 184 {
		return x*4 + 135
	}
	if x < 188 {
		return x*5 + 138
	}
	if x < 192 {
		return x*6 + 141
	}
	if x < 196 {
		return x*7 + 144
	}
	if x < 200 {
		return x*1 + 147
	}
	if x < 204 {
		return x*2 + 150
	}
	if x < 208 {
		return x*3 + 153
	}
	if x < 212 {
		return x*4 + 156
	}
	if x < 216 {
		return x*5 + 159
	}
	if x < 220 {
		return x*6 + 162
	}
	if x < 224 {
		return x*7 + 165
	}
	if x < 228 {
		return x*1 + 168
	}
	if x < 232 {
		return x*2 + 171
	}
	if x < 236 {
		return x*3 + 174
	}
	if x < 240 {
		return x*4 + 177
	}
	if x < 244 {
		return x*5 + 180
	}
	if x < 248 {
		return x*6 + 183
	}
	if x < 252 {
		return x*7 + 186
	}
	if x < 256 {
		return x*1 + 189
	}
	return -2
}

// classifyString 是 64 分支的字符串 switch。
func classifyString(s string) int {
	switch s {
	case "k000":
		return 0
	case "k001":
		return 1
	case "k002":
		return 2
	case "k003":
		return 3
	case "k004":
		return 4
	case "k005":
		return 5
	case "k006":
		return 6
	case "k007":
		return 7
	case "k008":
		return 8
	case "k009":
		return 9
	case "k010":
		return 10
	case "k011":
		return 11
	case "k012":
		return 12
	case "k013":
		return 13
	case "k014":
		return 14
	case "k015":
		return 15
	case "k016":
		return 16
	case "k017":
		return 17
	case "k018":
		return 18
	case "k019":
		return 19
	case "k020":
		return 20
	case "k021":
		return 21
	case "k022":
		return 22
	case "k023":
		return 23
	case "k024":
		return 24
	case "k025":
		return 25
	case "k026":
		return 26
	case "k027":
		return 27
	case "k028":
		return 28
	case "k029":
		return 29
	case "k030":
		return 30
	case "k031":
		return 31
	case "k032":
		return 32
	case "k033":
		return 33
	case "k034":
		return 34
	case "k035":
		return 35
	case "k036":
		return 36
	case "k037":
		return 37
	case "k038":
		return 38
	case "k039":
		return 39
	case "k040":
		return 40
	case "k041":
		return 41
	case "k042":
		return 42
	case "k043":
		return 43
	case "k044":
		return 44
	case "k045":
		return 45
	case "k046":
		return 46
	case "k047":
		return 47
	case "k048":
		return 48
	case "k049":
		return 49
	case "k050":
		return 50
	case "k051":
		return 51
	case "k052":
		return 52
	case "k053":
		return 53
	case "k054":
		return 54
	case "k055":
		return 55
	case "k056":
		return 56
	case "k057":
		return 57
	case "k058":
		return 58
	case "k059":
		return 59
	case "k060":
		return 60
	case "k061":
		return 61
	case "k062":
		return 62
	case "k063":
		return 63
	default:
		return -1
	}
}

// 以下两个参考函数不复用 switch/if 链，用于独立推导期望值。
func refSwitch(x int) int {
	if x < 0 || x >= 320 {
		return -1
	}
	return (x*x*7 + x*13 + 5) % 1009
}

func refIf(x int) int {
	if x < 0 {
		return -1
	}
	if x >= 256 {
		return -2
	}
	k := x / 4
	return x*((k%7)+1) + k*3
}

func main() {
	seed = loadSeed()
	rounds := 256 * tortureMultiplier()
	const span = 4160
	badSwitch, badIf, badStr := -1, -1, -1
	for r := 0; r < rounds; r++ {
		start := (int(seed) + r*97) % span
		for k := 0; k < span; k++ {
			x := -64 + (start+k)%span
			if got := classifySwitch(x); got != refSwitch(x) && badSwitch < 0 {
				badSwitch = x
			}
			if got := classifyIf(x); got != refIf(x) && badIf < 0 {
				badIf = x
			}
			if x >= 0 && x < 64 {
				if got := classifyString(fmt.Sprintf("k%03d", x)); got != x && badStr < 0 {
					badStr = x
				}
			}
		}
		if r%32 == 31 {
			gcStress()
		}
	}
	ck(badSwitch == -1, "large switch mismatch")
	ck(badIf == -1, "if-chain mismatch")
	ck(badStr == -1, "string switch mismatch")

	gcStress()
	ck(classifySwitch(0) == refSwitch(0), "switch boundary 0 mismatch")
	ck(classifySwitch(319) == refSwitch(319), "switch boundary 319 mismatch")
	ck(classifySwitch(320) == -1 && classifySwitch(-1) == -1, "switch default mismatch")
	ck(classifyIf(0) == 0, "if-chain boundary 0 mismatch")
	ck(classifyIf(3) == refIf(3) && classifyIf(4) == refIf(4), "if-chain boundary 3/4 mismatch")
	ck(classifyIf(255) == refIf(255), "if-chain boundary 255 mismatch")
	ck(classifyIf(256) == -2 && classifyIf(-1) == -1, "if-chain default mismatch")
	ck(classifyString("k000") == 0, "string switch first mismatch")
	ck(classifyString("k063") == 63, "string switch last mismatch")
	ck(classifyString("nope") == -1, "string switch default mismatch")

	finish("l11_switch_opt")
}
