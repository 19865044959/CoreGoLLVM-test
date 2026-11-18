// L09: C 嵌套结构体：vec2_t → entity_t → scene_t 三层嵌套，
// C 侧初始化/求和，Go 直接读写最内层字段；校验 sizeof/offsetof、
// 跨 GC 稳定性以及 Go 写入被 C 立即观察。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

/*
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>

typedef struct { int32_t x, y; } vec2_t;

typedef struct {
	vec2_t pos;
	vec2_t vel;
	int64_t id;
	char label[16];
} entity_t;

typedef struct {
	entity_t ents[4];
	int32_t count;
} scene_t;

static void c_scene_init(scene_t *s) {
	s->count = 4;
	for (int32_t i = 0; i < 4; i++) {
		s->ents[i].pos.x = i;
		s->ents[i].pos.y = -i;
		s->ents[i].vel.x = i * 2;
		s->ents[i].vel.y = -(i * 3);
		s->ents[i].id = 100 + i;
		snprintf(s->ents[i].label, sizeof s->ents[i].label, "ent-%d", (int)i);
	}
}

static int64_t c_scene_energy(const scene_t *s) {
	int64_t e = 0;
	for (int32_t i = 0; i < s->count; i++) {
		e += (int64_t)s->ents[i].pos.x + s->ents[i].pos.y;
		e += (int64_t)s->ents[i].vel.x + s->ents[i].vel.y;
		e += s->ents[i].id;
	}
	return e;
}

static void c_entity_translate(entity_t *e, int32_t dx, int32_t dy) {
	e->pos.x += dx;
	e->pos.y += dy;
}

static size_t c_vec2_size(void) { return sizeof(vec2_t); }
static size_t c_entity_size(void) { return sizeof(entity_t); }
static size_t c_scene_size(void) { return sizeof(scene_t); }
static size_t c_entity_off_vel(void) { return offsetof(entity_t, vel); }
static size_t c_entity_off_id(void) { return offsetof(entity_t, id); }
static size_t c_entity_off_label(void) { return offsetof(entity_t, label); }
static size_t c_scene_off_ents(void) { return offsetof(scene_t, ents); }
static size_t c_scene_off_count(void) { return offsetof(scene_t, count); }
*/
import "C"

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func main() {
	ck(uint64(unsafe.Sizeof(C.vec2_t{})) == uint64(C.c_vec2_size()), "sizeof vec2_t")
	ck(uint64(unsafe.Sizeof(C.entity_t{})) == uint64(C.c_entity_size()), "sizeof entity_t")
	ck(uint64(unsafe.Sizeof(C.scene_t{})) == uint64(C.c_scene_size()), "sizeof scene_t")
	ck(uint64(unsafe.Offsetof(C.entity_t{}.vel)) == uint64(C.c_entity_off_vel()), "offset entity.vel")
	ck(uint64(unsafe.Offsetof(C.entity_t{}.id)) == uint64(C.c_entity_off_id()), "offset entity.id")
	ck(uint64(unsafe.Offsetof(C.entity_t{}.label)) == uint64(C.c_entity_off_label()), "offset entity.label")
	ck(uint64(unsafe.Offsetof(C.scene_t{}.ents)) == uint64(C.c_scene_off_ents()), "offset scene.ents")
	ck(uint64(unsafe.Offsetof(C.scene_t{}.count)) == uint64(C.c_scene_off_count()), "offset scene.count")

	var s C.scene_t
	C.c_scene_init(&s)
	ck(int32(s.count) == 4, "scene count")
	var want int64
	for i := 0; i < 4; i++ {
		e := &s.ents[i]
		ck(int32(e.pos.x) == int32(i), fmt.Sprintf("pos.x i=%d", i))
		ck(int32(e.pos.y) == -int32(i), fmt.Sprintf("pos.y i=%d", i))
		ck(int32(e.vel.x) == int32(i*2), fmt.Sprintf("vel.x i=%d", i))
		ck(int32(e.vel.y) == -int32(i*3), fmt.Sprintf("vel.y i=%d", i))
		ck(int64(e.id) == int64(100+i), fmt.Sprintf("id i=%d", i))
		ck(C.GoString(&e.label[0]) == fmt.Sprintf("ent-%d", i), fmt.Sprintf("label i=%d", i))
		want += int64(i) - int64(i) + int64(i*2) - int64(i*3) + int64(100+i)
	}
	ck(int64(C.c_scene_energy(&s)) == want, "C nested energy")

	// Go 直接写最内层字段，C 求和立即反映。
	s.ents[2].vel.y = 77
	s.ents[3].pos.x = 40
	s.ents[3].id = 999
	want2 := want - int64(-2*3) + 77 - int64(3) + 40 - 103 + 999
	ck(int64(C.c_scene_energy(&s)) == want2, "C sees nested Go writes")

	// C 辅助函数修改嵌套实体。
	C.c_entity_translate(&s.ents[1], 10, 20)
	ck(int32(s.ents[1].pos.x) == 11 && int32(s.ents[1].pos.y) == 19, "C translate nested entity")
	want2 += 10 + 20
	ck(int64(C.c_scene_energy(&s)) == want2, "energy after C translate")

	// 跨多轮 GC。
	for round := 0; round < 64; round++ {
		gcStress()
		if round%8 == 0 {
			ck(int64(C.c_scene_energy(&s)) == want2, fmt.Sprintf("energy after GC round=%d", round))
			ck(C.GoString(&s.ents[0].label[0]) == "ent-0", fmt.Sprintf("label after GC round=%d", round))
		}
	}
	ck(int32(s.ents[3].pos.x) == 40 && int64(s.ents[3].id) == 999, "nested fields after GC")
	ck(float64(unsafe.Sizeof(s)) == float64(C.c_scene_size()), "scene value size")

	finish("l09_cgo_nested_struct")
}
