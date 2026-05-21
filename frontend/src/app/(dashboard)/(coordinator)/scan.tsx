import NfcManager, {NfcTech} from 'react-native-nfc-manager';
import { Pressable, View } from "react-native";
import { ThemedText } from '@/components/themed-text';
import { SafeAreaView } from 'react-native-safe-area-context';

export default function Scan(){
    const useNFC = async () => {
        try {
            await NfcManager.requestTechnology(NfcTech.Ndef);
            const tag = await NfcManager.getTag();
            console.warn('Tag found', tag);
        } catch (ex) {
            console.warn('Oops!', ex);
        } finally {
            NfcManager.cancelTechnologyRequest();
        }
    }
    return <SafeAreaView>
        <Pressable onPress={() => useNFC}>
            <ThemedText>Press me</ThemedText>
        </Pressable>
    </SafeAreaView>
}