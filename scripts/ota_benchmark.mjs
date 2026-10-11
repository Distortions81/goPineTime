// Run with node scripts/ota_benchmark.mjs; no Bluetooth or wall-clock sleeps.
import {benchmark} from './ota_simulation.mjs';
console.log(JSON.stringify(await benchmark(), null, 2));
