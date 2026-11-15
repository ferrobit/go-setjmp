#include <pthread.h>
#include <stdint.h>
#include <stdio.h>

#include "libsetjmp.h"

#define FOLD_PRIME 1099511628211ULL
#define THREADS 8
#define ITERATIONS 25
#define ROUNDS 200
#define STEPS 10

static int failures;
static int checks;
static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;

static void check(int condition, const char *name)
{
	pthread_mutex_lock(&lock);
	checks++;
	if (!condition) {
		failures++;
		fprintf(stderr, "FAIL: %s\n", name);
	}
	pthread_mutex_unlock(&lock);
}

static void *resume_sequence(void *argument)
{
	uint64_t *result = argument;
	for (int index = 0; index < STEPS; index++) {
		*result = *result * FOLD_PRIME ^ Next();
		Collect();
	}
	return NULL;
}

static void *compare_checksums(void *argument)
{
	uint64_t seed = (uint64_t)(uintptr_t)argument * 7919 + 1;
	for (int index = 0; index < ITERATIONS; index++) {
		uint64_t value = Checksum(seed + index, ROUNDS);
		check(value != 0 && value == Reference(seed + index, ROUNDS), "checksum matches reference");
	}
	return NULL;
}

int main(void)
{
	pthread_t threads[THREADS];
	uint64_t folded = 0;
	for (int index = 0; index < 3; index++) {
		check(Exported() == 0, "exported example");
	}
	resume_sequence(&folded);
	check(folded == Reference(1, STEPS), "sequence resumed across calls");
	pthread_create(&threads[0], NULL, resume_sequence, &folded);
	pthread_join(threads[0], NULL);
	check(folded == Reference(1, 2 * STEPS), "sequence resumed on another thread");
	for (uintptr_t index = 0; index < THREADS; index++) {
		pthread_create(&threads[index], NULL, compare_checksums, (void *)index);
	}
	for (int index = 0; index < THREADS; index++) {
		pthread_join(threads[index], NULL);
	}
	printf("%s: %d checks, %d failures\n", failures == 0 ? "PASS" : "FAIL", checks, failures);
	return failures == 0 ? 0 : 1;
}
