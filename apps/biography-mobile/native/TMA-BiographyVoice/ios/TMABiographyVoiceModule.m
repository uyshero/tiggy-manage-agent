#import "TMABiographyVoiceModule.h"

#if __has_include(<TMA_BiographyVoice/TMA_BiographyVoice-Swift.h>)
#import <TMA_BiographyVoice/TMA_BiographyVoice-Swift.h>
#else
#import "TMA_BiographyVoice-Swift.h"
#endif

@interface TMABiographyVoiceModule ()
@property(nonatomic, strong) TMABiographyVoiceCore *core;
@property(nonatomic, copy) UniModuleKeepAliveCallback eventCallback;
@end

@implementation TMABiographyVoiceModule

UNI_EXPORT_METHOD(@selector(configure:callback:))
UNI_EXPORT_METHOD(@selector(addEventListener:))
UNI_EXPORT_METHOD(@selector(removeEventListener))
UNI_EXPORT_METHOD(@selector(startListening:callback:))
UNI_EXPORT_METHOD(@selector(stopListening:callback:))
UNI_EXPORT_METHOD(@selector(cancelListening:))
UNI_EXPORT_METHOD(@selector(requestFollowup:callback:))
UNI_EXPORT_METHOD(@selector(setInterviewOrder:callback:))
UNI_EXPORT_METHOD(@selector(playText:callback:))
UNI_EXPORT_METHOD(@selector(cancelPlayback:))
UNI_EXPORT_METHOD(@selector(finishRecordingSession:))
UNI_EXPORT_METHOD(@selector(deleteRecording:callback:))
UNI_EXPORT_METHOD(@selector(dispose:))

- (instancetype)init {
    self = [super init];
    if (self) {
        _core = [[TMABiographyVoiceCore alloc] init];
        __weak typeof(self) weakSelf = self;
        _core.eventHandler = ^(NSDictionary *event) {
            if (weakSelf.eventCallback) {
                weakSelf.eventCallback(event, YES);
            }
        };
    }
    return self;
}

- (void)configure:(NSDictionary *)options callback:(UniModuleKeepAliveCallback)callback {
    [self.core configure:options completion:^(NSDictionary *result) { callback(result, NO); }];
}

- (void)addEventListener:(UniModuleKeepAliveCallback)callback {
    self.eventCallback = callback;
}

- (void)removeEventListener {
    self.eventCallback = nil;
}

- (void)startListening:(NSDictionary *)options callback:(UniModuleKeepAliveCallback)callback {
    [self.core startListening:options completion:^(NSDictionary *result) { callback(result, NO); }];
}

- (void)stopListening:(NSDictionary *)options callback:(UniModuleKeepAliveCallback)callback {
    [self.core stopListening:options completion:^(NSDictionary *result) { callback(result, NO); }];
}

- (void)cancelListening:(UniModuleKeepAliveCallback)callback {
    [self.core cancelListening:^(NSDictionary *result) { callback(result, NO); }];
}

- (void)requestFollowup:(NSDictionary *)options callback:(UniModuleKeepAliveCallback)callback {
    [self.core requestFollowup:options completion:^(NSDictionary *result) { callback(result, NO); }];
}

- (void)setInterviewOrder:(NSDictionary *)options callback:(UniModuleKeepAliveCallback)callback {
    [self.core setInterviewOrder:options completion:^(NSDictionary *result) { callback(result, NO); }];
}

- (void)playText:(NSDictionary *)options callback:(UniModuleKeepAliveCallback)callback {
    [self.core playText:options completion:^(NSDictionary *result) { callback(result, NO); }];
}

- (void)cancelPlayback:(UniModuleKeepAliveCallback)callback {
    [self.core cancelPlayback:^(NSDictionary *result) { callback(result, NO); }];
}

- (void)finishRecordingSession:(UniModuleKeepAliveCallback)callback {
    [self.core finishRecordingSession:^(NSDictionary *result) { callback(result, NO); }];
}

- (void)deleteRecording:(NSDictionary *)options callback:(UniModuleKeepAliveCallback)callback {
    [self.core deleteRecording:options completion:^(NSDictionary *result) { callback(result, NO); }];
}

- (void)dispose:(UniModuleKeepAliveCallback)callback {
    [self.core dispose:^(NSDictionary *result) { callback(result, NO); }];
    self.eventCallback = nil;
}

@end
