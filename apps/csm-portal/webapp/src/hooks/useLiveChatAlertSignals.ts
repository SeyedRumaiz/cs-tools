// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

import { useEffect, useRef } from "react";

const TITLE_FLASH_INTERVAL_MS = 1000;
const BEEP_FREQUENCIES_HZ = [880, 1175];
const BEEP_NOTE_SECONDS = 0.18;

let audioContext: AudioContext | null = null;

function getAudioContext(): AudioContext | null {
  if (audioContext) return audioContext;
  const Ctor = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!Ctor) return null;
  audioContext = new Ctor();
  return audioContext;
}

// Browsers keep audio suspended until the user has interacted with the page,
// so the first click or key press resumes it for every later alert.
function unlockAudio(): void {
  void getAudioContext()?.resume();
}

function playAlertSound(): void {
  const ctx = getAudioContext();
  if (!ctx || ctx.state !== "running") return;
  BEEP_FREQUENCIES_HZ.forEach((frequency, i) => {
    const start = ctx.currentTime + i * BEEP_NOTE_SECONDS;
    const oscillator = ctx.createOscillator();
    const gain = ctx.createGain();
    oscillator.type = "sine";
    oscillator.frequency.value = frequency;
    gain.gain.setValueAtTime(0.0001, start);
    gain.gain.exponentialRampToValueAtTime(0.25, start + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, start + BEEP_NOTE_SECONDS);
    oscillator.connect(gain).connect(ctx.destination);
    oscillator.start(start);
    oscillator.stop(start + BEEP_NOTE_SECONDS);
  });
}

/**
 * Makes new live-chat requests noticeable when the engineer is looking at
 * another tab: plays a short sound whenever a case id appears that was not
 * pending before, and, while the tab is hidden, flashes the tab title with
 * the pending count until it is viewed or the requests are gone.
 *
 * Sound needs the page to have had at least one click or key press (a
 * browser rule), so an engineer who has never touched the page since loading
 * it gets the title flash only.
 */
export function useLiveChatAlertSignals(pendingCaseIds: string[]): void {
  const knownIdsRef = useRef<Set<string>>(new Set());
  const idsKey = pendingCaseIds.join("|");
  const pendingCount = pendingCaseIds.length;

  useEffect(() => {
    window.addEventListener("pointerdown", unlockAudio, { once: true });
    window.addEventListener("keydown", unlockAudio, { once: true });
    return () => {
      window.removeEventListener("pointerdown", unlockAudio);
      window.removeEventListener("keydown", unlockAudio);
    };
  }, []);

  useEffect(() => {
    const current = new Set(idsKey === "" ? [] : idsKey.split("|"));
    const hasNew = [...current].some((id) => !knownIdsRef.current.has(id));
    knownIdsRef.current = current;
    if (hasNew) playAlertSound();
  }, [idsKey]);

  useEffect(() => {
    if (pendingCount === 0) return;
    const originalTitle = document.title;
    let showAlert = false;
    const apply = (): void => {
      if (!document.hidden) {
        document.title = originalTitle;
        return;
      }
      showAlert = !showAlert;
      document.title = showAlert ? `(${pendingCount}) Live chat requested` : originalTitle;
    };
    const id = setInterval(apply, TITLE_FLASH_INTERVAL_MS);
    document.addEventListener("visibilitychange", apply);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", apply);
      document.title = originalTitle;
    };
  }, [pendingCount]);
}
