import { ReactNode } from 'react';
import {
  ImageBackground,
  Platform,
  ScrollView,
  StyleProp,
  StyleSheet,
  StatusBar,
  View,
  ViewStyle,
} from 'react-native';

import appBackground from '../../assets/app-background.jpg';

interface AppSurfaceProps {
  children: ReactNode;
  contentStyle?: StyleProp<ViewStyle>;
}

interface GlassPanelProps {
  children: ReactNode;
  style?: StyleProp<ViewStyle>;
}

export function AppSurface({ children, contentStyle }: AppSurfaceProps) {
  return (
    <ImageBackground source={appBackground} resizeMode="cover" style={styles.background}>
      <View style={styles.imageMask} />
      <View style={styles.safeArea}>
        <ScrollView
          bounces={false}
          contentContainerStyle={[styles.content, contentStyle]}
          keyboardShouldPersistTaps="handled"
          showsVerticalScrollIndicator={false}
        >
          {children}
        </ScrollView>
      </View>
    </ImageBackground>
  );
}

export function GlassPanel({ children, style }: GlassPanelProps) {
  return <View style={[styles.glassPanel, style]}>{children}</View>;
}

const styles = StyleSheet.create({
  background: {
    flex: 1,
    backgroundColor: '#06101c',
  },
  imageMask: {
    position: 'absolute',
    top: 0,
    right: 0,
    bottom: 0,
    left: 0,
    backgroundColor: 'rgba(2, 7, 13, 0.56)',
  },
  safeArea: {
    flex: 1,
    paddingTop: Platform.OS === 'android' ? StatusBar.currentHeight ?? 0 : 0,
  },
  content: {
    flexGrow: 1,
    justifyContent: 'center',
    paddingHorizontal: 22,
    paddingVertical: 30,
  },
  glassPanel: {
    width: '100%',
    maxWidth: 430,
    alignSelf: 'center',
    overflow: 'hidden',
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(255, 255, 255, 0.18)',
    backgroundColor: 'rgba(11, 24, 38, 0.68)',
    shadowColor: '#000000',
    shadowOffset: { width: 0, height: 24 },
    shadowOpacity: 0.32,
    shadowRadius: 34,
    elevation: 12,
  },
});
