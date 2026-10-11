#!/usr/bin/env python3
"""Build the experimental cooperative NimBLE archive from pinned local sources.

The InfiniTime checkout supplies its Apache-licensed NimBLE tree. Nordic MDK
and ARM CMSIS headers come from the pinned TinyGo toolchain. No downloads or
device writes are performed. This is an integration build, not a release path.
"""
import argparse
import pathlib
import re
import subprocess
import tempfile
from ble_crypto_sources import prepare_crypto

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--infinitime',type=pathlib.Path,required=True)
    p.add_argument('--tinygo-root',type=pathlib.Path,required=True)
    args=p.parse_args()
    cache=pathlib.Path(subprocess.check_output([str(args.tinygo_root/'bin/tinygo'),'env','GOCACHE'],text=True).strip())
    libc_config=cache/'picolibc-thumbv7em-unknown-unknown-eabi-cortex-m4/include'
    if not (libc_config/'picolibc.h').is_file():
        raise SystemExit('Build the plain PineTime target once to initialize TinyGo libc headers')
    root=pathlib.Path(__file__).resolve().parent.parent
    vendor=args.infinitime.resolve()
    revision=subprocess.check_output(['git','-C',str(vendor),'rev-parse','HEAD'],text=True).strip()
    if revision!='6c119eb52206b580b556b41633dddc1e1b66a8da':
        raise SystemExit('Unexpected InfiniTime revision; review NimBLE input before changing pin')
    dirty=subprocess.check_output(['git','-C',str(vendor),'status','--porcelain','--','src/CMakeLists.txt','src/libs/mynewt-nimble'],text=True)
    if dirty.strip():
        raise SystemExit('NimBLE input checkout has local changes')
    version=subprocess.check_output([str(args.tinygo_root/'bin/tinygo'),'version'],text=True)
    if not version.startswith('tinygo version 0.42.0 '):
        raise SystemExit('This radio port requires the audited TinyGo 0.42.0 toolchain')
    nimble=vendor/'src/libs/mynewt-nimble'
    port=root/'internal/ble_nimble/port'
    out=root/'build/ble';out.mkdir(parents=True,exist_ok=True)
    cmake=(vendor/'src/CMakeLists.txt').read_text()
    section=cmake.split('set(NIMBLE_SRC',1)[1].split(')',1)[0]
    sources=list(dict.fromkeys(vendor/'src'/s for s in re.findall(r'libs/[^\s]+\.c',section) if '/npl/freertos/' not in s))
    sources=[s for s in sources if s.name not in ('ble_hs_stop.c', 'ble_store_ram.c', 'ble_sm.c', 'ble_gatts.c', 'ble_uuid.c')]
    sources += [nimble/'porting/nimble/src/nimble_port.c',port/'npl.c',port/'service.c',port/'ancs.c',port/'alloc.c',port/'stop.c',port/'sm.c',port/'gatts.c',port/'bond.c',port/'bond_flash.c',port/'privacy.c',port/'uuid.c']
    include=[port/'include',libc_config,nimble/'porting/nimble/include',nimble/'nimble/include',
             nimble/'nimble/host/include',nimble/'nimble/controller/include',
             nimble/'nimble/host/src',
             nimble/'nimble/host/services/gap/include',nimble/'nimble/host/services/gatt/include',
             nimble/'nimble/host/util/include',nimble/'nimble/transport/ram/include',
             nimble/'nimble/host/store/ram/include',nimble/'nimble/host/store/ram/src',
             nimble/'nimble/drivers/nrf52/include',nimble/'ext/tinycrypt/include',
             args.tinygo_root/'lib/nrfx/mdk',
             args.tinygo_root/'lib/CMSIS/CMSIS/Include',
             args.tinygo_root/'lib/picolibc/newlib/libc/tinystdio',
             args.tinygo_root/'lib/picolibc/newlib/libc/include']
    flags=['--target=arm-none-eabi','-mcpu=cortex-m4','-mthumb','-mfloat-abi=soft','-Oz',
           '-fstack-usage','-ffunction-sections','-fdata-sections','-fno-short-enums','-include',str(port/'config.h')]
    flags += [flag for path in include for flag in ['-I',str(path)]]
    # Never reuse archive members from an older source list or publish a
    # partially built archive. This folder contains only generated objects.
    with tempfile.TemporaryDirectory(prefix='nimble-',dir=out) as temp:
        sources += prepare_crypto(nimble, pathlib.Path(temp))
        objects=[]
        for i,source in enumerate(sources):
            obj=pathlib.Path(temp)/f'{i}.o'
            subprocess.run(['clang',*flags,'-c',str(source),'-o',str(obj)],check=True)
            objects.append(str(obj))
        archive=pathlib.Path(temp)/'libgopineble.a'
        subprocess.run(['llvm-ar','rcs',str(archive),*objects],check=True)
        archive.replace(out/'libgopineble.a')
        (out/'stack-usage.txt').write_text(''.join(p.read_text() for p in sorted(pathlib.Path(temp).glob('*.su'))))
    print(f'Built {len(sources)} objects into {out}/libgopineble.a')

if __name__=='__main__':main()
