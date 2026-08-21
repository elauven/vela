// runtime-fixture-dsl.mjs - shared declarative step semantics for runtime tests.
//
// Environment adapters provide DOM operations; this module owns step decoding,
// assertion wording, storage seeding, and the post-action settle contract.

const stepKeys = [
  'seedStorage', 'assertText', 'assertHTML', 'assertValue', 'assertStorage',
  'assertCount', 'assertFocused', 'assertStyle', 'focus', 'click', 'dblclick',
  'submit', 'input', 'change', 'blur', 'keydown', 'tick',
];

function kindOf(step) {
  return stepKeys.find(key => key in step);
}

export function fixtureStorage(steps) {
  const storage = {};
  for (const step of steps) {
    if (step.seedStorage && typeof step.seedStorage === 'object') {
      for (const [key, value] of Object.entries(step.seedStorage)) {
        storage[key] = String(value);
      }
    }
  }
  return storage;
}

function quoted(value) {
  return JSON.stringify(value);
}

export async function replayFixtureSteps(steps, adapter) {
  const failures = [];

  for (let index = 0; index < steps.length; index++) {
    const step = steps[index];
    const tag = 'step ' + index;
    const kind = kindOf(step);
    try {
      switch (kind) {
        case 'seedStorage':
          break;
        case 'assertText': {
          const got = await adapter.text(step.assertText);
          if (got !== step.equals) {
            failures.push(tag + ': text of ' + quoted(step.assertText) +
              ' = ' + quoted(got) + ', want ' + quoted(step.equals));
          }
          break;
        }
        case 'assertHTML': {
          const got = await adapter.html(step.assertHTML);
          if (got == null || !got.includes(step.contains ?? '')) {
            failures.push(tag + ': html of ' + quoted(step.assertHTML) +
              ' = ' + quoted(got) + ', want substring ' +
              quoted(step.contains ?? ''));
          }
          break;
        }
        case 'assertValue': {
          const got = await adapter.value(step.assertValue);
          if (got !== step.equals) {
            failures.push(tag + ': value of ' + quoted(step.assertValue) +
              ' = ' + quoted(got) + ', want ' + quoted(step.equals));
          }
          break;
        }
        case 'assertStorage': {
          const got = await adapter.storage(step.assertStorage);
          if (got == null || !got.includes(step.contains ?? '')) {
            failures.push(tag + ': storage[' + quoted(step.assertStorage) +
              '] = ' + quoted(got) + ', want substring ' +
              quoted(step.contains ?? ''));
          }
          break;
        }
        case 'assertCount': {
          const got = await adapter.count(step.assertCount);
          if (got !== step.equals) {
            failures.push(tag + ': count of ' + quoted(step.assertCount) +
              ' = ' + got + ', want ' + step.equals);
          }
          break;
        }
        case 'assertFocused': {
          if (!(await adapter.focused(step.assertFocused))) {
            failures.push(tag + ': expected focus on ' + step.assertFocused);
          }
          break;
        }
        case 'assertStyle': {
          const got = await adapter.style(step.assertStyle, step.property);
          if (got !== step.equals) {
            failures.push(tag + ': style ' + step.property + ' of ' +
              step.assertStyle + ' = ' + quoted(got) + ', want ' +
              quoted(step.equals));
          }
          break;
        }
        case 'focus':
          await adapter.focus(step.focus);
          break;
        case 'click':
          await adapter.click(step.click);
          await adapter.settle();
          break;
        case 'dblclick':
          await adapter.dblclick(step.dblclick);
          await adapter.settle();
          break;
        case 'submit':
          await adapter.submit(step.submit);
          await adapter.settle();
          break;
        case 'input':
          await adapter.input(step.input, step.value ?? '');
          await adapter.settle();
          break;
        case 'change':
          await adapter.change(step.change, step);
          await adapter.settle();
          break;
        case 'blur':
          await adapter.blur(step.blur);
          await adapter.settle();
          break;
        case 'keydown':
          await adapter.keydown(step.keydown, step.key ?? '');
          await adapter.settle();
          break;
        case 'tick':
          await adapter.settle();
          break;
        default:
          failures.push(tag + ': unknown step ' + JSON.stringify(step));
      }
    } catch (error) {
      failures.push(tag + ': ' + String(error && (error.stack || error.message) || error));
    }
  }

  return failures;
}
