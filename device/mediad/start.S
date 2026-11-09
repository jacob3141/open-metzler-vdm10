@ Startup code for programs linked against the uClibc of the door station.
@ __uClibc_main(main, argc, argv, app_init, app_fini, rtld_fini, stack_end)
	.text
	.arm
	.global _start
	.type _start, %function
_start:
	mov	fp, #0
	mov	lr, #0
	mov	r5, r0
	ldr	r1, [sp]
	add	r2, sp, #4
	mov	r6, sp
	bic	sp, sp, #7
	sub	sp, sp, #16
	mov	r3, #0
	str	r3, [sp, #0]
	str	r5, [sp, #4]
	str	r6, [sp, #8]
	ldr	r0, =main
	bl	__uClibc_main
	b	.
	.ltorg
	.section .note.GNU-stack,"",%progbits
